package discovery

import (
	"testing"

	"github.com/google/uuid"
)

func mkClassified(method, endpoint, identity, op string, rowCount int64, dir string, sampleURL, clientIP string, status int) classified {
	return classified{
		rawSignal: rawSignal{
			Method:           method,
			Endpoint:         endpoint,
			ObservationPoint: op,
			RowCount:         rowCount,
			SampleURL:        sampleURL,
			ClientIP:         clientIP,
			SampleStatus:     status,
			FirstSeenUnix:    1700000000,
			LastSeenUnix:     1700000060,
			AvgDurationUs:    100,
			K8sNamespace:     "techmart",
			K8sService:       "orders",
		},
		EnvKind:          "k8s",
		ServiceIdentity:  identity,
		TrafficDirection: dir,
	}
}

func TestMergeSameKey(t *testing.T) {
	n := buildNormalizer(t)

	in := []classified{
		mkClassified("GET", "/orders/123/items", "k8s:techmart/orders", tapServerSide, 5, "external",
			"https://x/orders/123/items", "10.50.2.10", 200),
		mkClassified("GET", "/orders/456/items", "k8s:techmart/orders", tapServerSideProcess, 3, "external",
			"https://x/orders/456/items", "10.50.2.11", 200),
	}

	out := MergeAndNormalize(in, n, uuid.New())
	if len(out) != 1 {
		t.Fatalf("expected 1 merged row, got %d: %+v", len(out), out)
	}
	row := out[0]

	if row.Key.NormalizedPath != "/orders/{id}/items" {
		t.Errorf("normalized_path = %q, want /orders/{id}/items", row.Key.NormalizedPath)
	}
	if row.Key.Method != "GET" {
		t.Errorf("method = %q, want GET", row.Key.Method)
	}
	if row.RowCount != 8 {
		t.Errorf("row_count = %d, want 8", row.RowCount)
	}
	if row.FlowCount != 2 {
		t.Errorf("flow_count = %d, want 2", row.FlowCount)
	}
	if row.ExternalFlows != 8 {
		t.Errorf("external_flows = %d, want 8", row.ExternalFlows)
	}
	if row.InternalFlows != 0 {
		t.Errorf("internal_flows = %d, want 0", row.InternalFlows)
	}
	if len(row.RawPathSamples) != 2 {
		t.Errorf("raw_path_samples count = %d, want 2 (2 distinct sample URLs)", len(row.RawPathSamples))
	}
	if row.DistinctClientCount != 2 {
		t.Errorf("distinct_client_count = %d, want 2", row.DistinctClientCount)
	}
}

func TestMergeKeyDistinguishesPaths(t *testing.T) {
	n := buildNormalizer(t)
	in := []classified{
		mkClassified("GET", "/products/1.0.0/items", "k8s:techmart/products", tapServerSide, 5, "external", "u1", "10.50.2.10", 200),
		mkClassified("GET", "/orders/1.0.0/items", "k8s:techmart/orders", tapServerSide, 3, "external", "u2", "10.50.2.10", 200),
	}
	out := MergeAndNormalize(in, n, uuid.New())
	if len(out) != 2 {
		t.Fatalf("expected 2 merged rows, got %d", len(out))
	}
	// Sorted by service identity then path: orders before products.
	if out[0].Key.ServiceIdentity != "k8s:techmart/orders" {
		t.Errorf("first row identity = %q, want orders first", out[0].Key.ServiceIdentity)
	}
}

func TestMergeRawPathSamplesCappedAt20(t *testing.T) {
	n := buildNormalizer(t)
	in := make([]classified, 0, 30)
	for i := 0; i < 30; i++ {
		c := mkClassified("GET", "/users/123", "k8s:techmart/users", tapServerSide, 1, "external",
			"distinct-sample-"+string(rune('a'+i)), "10.50.2.10", 200)
		in = append(in, c)
	}
	out := MergeAndNormalize(in, n, uuid.New())
	if len(out) != 1 {
		t.Fatalf("expected 1 merged row, got %d", len(out))
	}
	if got := len(out[0].RawPathSamples); got > 20 {
		t.Errorf("raw_path_samples = %d entries, want ≤ 20", got)
	}
}

func TestCollectServicesDeduplicates(t *testing.T) {
	n := buildNormalizer(t)
	rows := MergeAndNormalize([]classified{
		mkClassified("GET", "/a", "k8s:techmart/orders", tapServerSide, 1, "external", "u", "c", 200),
		mkClassified("GET", "/b", "k8s:techmart/orders", tapServerSide, 1, "external", "u", "c", 200),
		mkClassified("POST", "/c", "k8s:techmart/products", tapServerSide, 1, "external", "u", "c", 200),
	}, n, uuid.New())

	svcs := CollectServices(rows)
	if len(svcs) != 2 {
		t.Errorf("got %d services, want 2: %+v", len(svcs), svcs)
	}
}
