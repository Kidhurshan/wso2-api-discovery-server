package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/deepflow"
	"github.com/wso2/api-discovery-server/internal/store"
)

// Pipeline runs one Phase 1 cycle when Run is called.
type Pipeline struct {
	cfg            *config.Config
	log            *zap.Logger
	deepflow       deepflow.Client
	normalizer     *Normalizer
	noisePatterns  []string        // substring (CONTAINS) match
	noiseExact     map[string]bool // exact match
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
	// Noise: substring patterns (path_patterns) and exact matches (path_exact).
	// path_patterns is contains-check so "/health" drops "/orders/1.0.0/health"
	// too. path_exact is equality so "/" only drops the literal root.
	exact := make(map[string]bool, len(cfg.Discovery.NoiseFilter.PathExact))
	for _, p := range cfg.Discovery.NoiseFilter.PathExact {
		exact[p] = true
	}

	var domainSet map[string]bool
	if domains := cfg.Discovery.NoiseFilter.ExcludedDomains; len(domains) > 0 {
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
		noisePatterns:  cfg.Discovery.NoiseFilter.PathPatterns,
		noiseExact:     exact,
		noiseDomainSet: domainSet,
		serviceRepo:    serviceRepo,
		discoveredRepo: discoveredRepo,
		pipelineRepo:   pipelineRepo,
	}
}

// isNoise returns true when path matches the noise filter and should be dropped.
//   - exact equality with any entry in PathExact
//   - contains-substring with any entry in PathPatterns
func (p *Pipeline) isNoise(path string) bool {
	if p.noiseExact[path] {
		return true
	}
	for _, sub := range p.noisePatterns {
		if strings.Contains(path, sub) {
			return true
		}
	}
	return false
}

// Run executes one cycle: query DeepFlow, classify, merge, upsert.
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
		NoisePorts:      p.cfg.Discovery.NoiseFilter.ExcludedPorts,
		NoiseDomains:    p.cfg.Discovery.NoiseFilter.ExcludedDomains,
		SkipInternal:    p.cfg.Discovery.SkipInternal,
		MinObservations: p.cfg.Discovery.MinObservations,
		MaxSignatures:   p.cfg.Discovery.MaxSignaturesPerWindow,
	})

	rows, err := p.deepflow.Query(ctx, "flow_log", sql)
	if err != nil {
		return fmt.Errorf("deepflow query: %w", err)
	}
	cycleLog.Info("deepflow rows fetched", zap.Int("count", len(rows)))

	// 2. Decode + apply Go-side noise filter.
	signals := make([]rawSignal, 0, len(rows))
	noiseDropped := 0
	for _, r := range rows {
		s := fromRow(r)
		if p.isNoise(s.Endpoint) {
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

	// 4. Normalize + merge.
	merged := MergeAndNormalize(classified, p.normalizer, cycleID)
	cycleLog.Info("rows merged", zap.Int("count", len(merged)))

	// 5. Persist services first so we have UUIDs to attach to discovered_apis.
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

	// 6. Build the discovered upserts using the freshly minted UUIDs.
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

	// 7. Update last-success state.
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
