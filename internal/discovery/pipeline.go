package discovery

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/deepflow"
	"github.com/wso2/api-discovery-server/internal/store"
)

// Pipeline runs one Phase 1 cycle when Run is called.
//
// Construction:
//
//	p := discovery.NewPipeline(cfg, logger, deepflowClient, serviceRepo,
//	                          discoveredRepo, pipelineRepo)
//	err := p.Run(ctx, cycleID)
//
// The pipeline never holds long-lived state between cycles — fresh window,
// fresh classification each call. State that must persist (last_success,
// breaker history) lives on PipelineRepo.
type Pipeline struct {
	cfg            *config.Config
	log            *zap.Logger
	deepflow       deepflow.Client
	normalizer     *Normalizer
	noisePathRe    *regexp.Regexp  // compiled noise filter; nil if disabled
	noiseDomainSet map[string]bool // O(1) domain blocklist; nil if disabled
	serviceRepo    *store.ServiceRepo
	discoveredRepo *store.DiscoveredRepo
	pipelineRepo   *store.PipelineRepo
}

// NewPipeline wires the dependencies. cfg.Validate() must have already run
// so the normalizer's compiled regexes are populated.
func NewPipeline(
	cfg *config.Config,
	log *zap.Logger,
	df deepflow.Client,
	serviceRepo *store.ServiceRepo,
	discoveredRepo *store.DiscoveredRepo,
	pipelineRepo *store.PipelineRepo,
) *Pipeline {
	// Compile the noise-path regex once; nil means "no filter".
	// Bad patterns are caught by config.Validate at startup, so MustCompile
	// would also be safe here — but use Compile to keep cycle-loop tests
	// flexible (they may pass a Config without prior Validate).
	var noiseRe *regexp.Regexp
	if pat := cfg.Discovery.NoiseFilter.PathPattern; pat != "" {
		if compiled, err := regexp.Compile(pat); err == nil {
			noiseRe = compiled
		} else {
			log.Warn("noise path regex failed to compile; filter disabled",
				zap.String("pattern", pat), zap.Error(err))
		}
	}

	var domainSet map[string]bool
	if domains := cfg.Discovery.NoiseFilter.Domains; len(domains) > 0 {
		domainSet = make(map[string]bool, len(domains))
		for _, d := range domains {
			domainSet[d] = true
		}
	}

	return &Pipeline{
		cfg:            cfg,
		log:            log,
		deepflow:       df,
		normalizer:     NewFromConfig(&cfg.Discovery),
		noisePathRe:    noiseRe,
		noiseDomainSet: domainSet,
		serviceRepo:    serviceRepo,
		discoveredRepo: discoveredRepo,
		pipelineRepo:   pipelineRepo,
	}
}

// Run executes one cycle: query DeepFlow, classify, merge, upsert.
//
// Returns nil even when the window is empty — that's a valid state (no
// traffic). It returns an error only on infrastructure failures
// (deepflow query, db write).
func (p *Pipeline) Run(ctx context.Context, cycleID uuid.UUID) error {
	cycleLog := p.log.With(zap.String("cycle_id", cycleID.String()))
	start := time.Now()

	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-time.Duration(p.cfg.Discovery.WindowMinutes) * time.Minute)

	cycleLog.Info("phase 1 cycle starting",
		zap.Time("window_start", windowStart),
		zap.Time("window_end", windowEnd),
	)

	// 1. Query DeepFlow.
	sql := deepflow.BuildPerFlowSQL(deepflow.PerFlowQuery{
		WindowStart:     windowStart,
		WindowEnd:       windowEnd,
		StatusMin:       p.cfg.Discovery.StatusMin,
		StatusMax:       p.cfg.Discovery.StatusMax,
		NoisePorts:      p.cfg.Discovery.NoiseFilter.Ports,
		NoiseDomains:    p.cfg.Discovery.NoiseFilter.Domains,
		SkipInternal:    p.cfg.Discovery.SkipInternal,
		MinObservations: p.cfg.Discovery.MinObservations,
		MaxSignatures:   p.cfg.Discovery.MaxSignaturesPerWindow,
	})

	rows, err := p.deepflow.Query(ctx, "flow_log", sql)
	if err != nil {
		return fmt.Errorf("deepflow query: %w", err)
	}
	cycleLog.Info("deepflow rows fetched", zap.Int("count", len(rows)))

	// 2. Decode + apply Go-side noise filters. Both moved here from SQL
	//    because the DeepFlow querier rejects them — match() isn't
	//    supported and request_domain WHERE conflicts with the SELECT
	//    alias. See project memory: deepflow_querier_strategy.
	signals := make([]rawSignal, 0, len(rows))
	noiseDropped := 0
	for _, r := range rows {
		s := fromRow(r)
		if p.noisePathRe != nil && p.noisePathRe.MatchString(s.Endpoint) {
			noiseDropped++
			continue
		}
		if p.noiseDomainSet != nil && p.noiseDomainSet[s.RequestDomain] {
			noiseDropped++
			continue
		}
		signals = append(signals, s)
	}
	if noiseDropped > 0 {
		cycleLog.Debug("noise rows filtered Go-side", zap.Int("dropped", noiseDropped))
	}

	// 3. Classify (drops env_kind=skip rows per spec §3 truth table).
	classified := classifyAndDrop(signals)

	// Optional: drop internal-only direction when configured.
	if p.cfg.Discovery.SkipInternal {
		filtered := classified[:0]
		for _, c := range classified {
			if c.TrafficDirection != "internal" {
				filtered = append(filtered, c)
			}
		}
		classified = filtered
	}

	cycleLog.Info("rows classified",
		zap.Int("input", len(signals)),
		zap.Int("kept", len(classified)),
	)

	// 3. Normalize + merge.
	merged := MergeAndNormalize(classified, p.normalizer, cycleID)
	cycleLog.Info("rows merged", zap.Int("count", len(merged)))

	// 4. Persist services first so we have UUIDs to attach to discovered_apis.
	serviceCandidates := CollectServices(merged)
	serviceUpserts := make([]store.ServiceUpsert, 0, len(serviceCandidates))
	for _, c := range serviceCandidates {
		serviceUpserts = append(serviceUpserts, store.ServiceUpsert{
			ServiceIdentity: c.ServiceIdentity,
			EnvKind:         c.EnvKindString(),
			FirstSeenAt:     c.FirstSeenAt,
			LastSeenAt:      c.LastSeenAt,
		})
	}
	identityToID, err := p.serviceRepo.EnsureServices(ctx, serviceUpserts)
	if err != nil {
		return fmt.Errorf("ensure services: %w", err)
	}

	// 5. Build the discovered upserts using the freshly minted UUIDs.
	upserts := make([]store.DiscoveredUpsert, 0, len(merged))
	for _, m := range merged {
		serviceID, ok := identityToID[m.Key.ServiceIdentity]
		if !ok {
			cycleLog.Warn("dropping discovered row for unknown service",
				zap.String("service_identity", m.Key.ServiceIdentity),
				zap.String("method", m.Key.Method),
				zap.String("path", m.Key.NormalizedPath),
			)
			continue
		}
		upserts = append(upserts, store.DiscoveredUpsert{
			ServiceID:             serviceID,
			Method:                m.Key.Method,
			NormalizedPath:        m.Key.NormalizedPath,
			RawPathSamples:        m.RawPathSamples,
			FirstSeenAt:           m.FirstSeenAt,
			LastSeenAt:            m.LastSeenAt,
			ObservationCount:      m.RowCount,
			FlowCount:             m.FlowCount,
			DistinctClientCount:   m.DistinctClientCount,
			DistinctClientsSample: m.DistinctClientsSample,
			StatusCodes:           m.StatusCodes,
			AvgDurationUs:         m.AvgDurationUs,
			RequestDomain:         m.RequestDomain,
			InternalFlows:         m.InternalFlows,
			ExternalFlows:         m.ExternalFlows,
			SamplePod:             m.SamplePod,
			SampleWorkload:        m.SampleWorkload,
			NormalizationVersion:  m.NormalizationVersion,
			LastWindowID:          m.LastWindowID,
		})
	}

	if err := p.discoveredRepo.BatchUpsert(ctx, upserts); err != nil {
		return fmt.Errorf("upsert discovered_apis: %w", err)
	}

	// 6. Update last-success state.
	if err := p.pipelineRepo.UpdatePhase1Success(ctx, windowStart, windowEnd); err != nil {
		return fmt.Errorf("update pipeline state: %w", err)
	}

	cycleLog.Info("phase 1 cycle complete",
		zap.Int("services", len(serviceUpserts)),
		zap.Int("discovered", len(upserts)),
		zap.Duration("elapsed", time.Since(start)),
	)
	return nil
}
