package discovery

import "testing"

func TestClassifyK8s(t *testing.T) {
	c := classify(rawSignal{
		Method:           "GET",
		Endpoint:         "/orders/1.0.0/items",
		ObservationPoint: tapServerSideProcess,
		InstanceTypeS:    instanceTypeK8sPod,
		K8sService:       "orders",
		K8sNamespace:     "techmart",
		ServerPort:       8080,
		ServerIP:         "10.42.0.171",
	})

	if c.EnvKind != "k8s" {
		t.Errorf("env_kind = %q, want k8s", c.EnvKind)
	}
	if c.ServiceIdentity != "k8s:techmart/orders" {
		t.Errorf("identity = %q, want k8s:techmart/orders", c.ServiceIdentity)
	}
}

func TestClassifyLegacy(t *testing.T) {
	c := classify(rawSignal{
		Method:           "POST",
		Endpoint:         "/payments/1.0.0/charges",
		ObservationPoint: tapServerSide,
		InstanceTypeS:    instanceTypeChost,
		ServerIP:         "10.50.1.11",
		ServerPort:       8083,
	})

	if c.EnvKind != "legacy" {
		t.Errorf("env_kind = %q, want legacy", c.EnvKind)
	}
	if c.ServiceIdentity != "host:10.50.1.11:8083" {
		t.Errorf("identity = %q, want host:10.50.1.11:8083", c.ServiceIdentity)
	}
}

func TestClassifySkipsK8sWithoutIdentity(t *testing.T) {
	// Pod-typed but missing namespace → skip (avoids the agent-X degenerate
	// identity bug from earlier prototypes; spec §8 calls this out).
	c := classify(rawSignal{
		Method:           "GET",
		Endpoint:         "/something",
		ObservationPoint: tapServerSide,
		InstanceTypeS:    instanceTypeK8sPod,
		K8sService:       "orders",
		K8sNamespace:     "",
		ServerIP:         "10.42.0.5",
		ServerPort:       8080,
	})
	if c.EnvKind != "skip" {
		t.Errorf("env_kind = %q, want skip", c.EnvKind)
	}
}

func TestDirection(t *testing.T) {
	cases := []struct {
		name string
		in   rawSignal
		want string
	}{
		{"client tap, tracked client → internal",
			rawSignal{ObservationPoint: tapClientSideProcess, InstanceTypeC: instanceTypeK8sPod}, "internal"},
		{"client tap, untracked client → external",
			rawSignal{ObservationPoint: tapClientSide, InstanceTypeC: 99}, "external"},
		{"server tap, instance_type_client = 255 → external",
			rawSignal{ObservationPoint: tapServerSide, InstanceTypeC: instanceTypeInternetUnknwn}, "external"},
		{"server tap, instance_type_client = pod → internal (default branch)",
			rawSignal{ObservationPoint: tapServerSide, InstanceTypeC: instanceTypeK8sPod}, "internal"},
		{"server tap, no client info → internal (default)",
			rawSignal{ObservationPoint: tapServerSide}, "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := directionFor(tc.in); got != tc.want {
				t.Errorf("directionFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyAndDropFiltersSkip(t *testing.T) {
	in := []rawSignal{
		{Method: "GET", Endpoint: "/keep", ObservationPoint: tapServerSide, InstanceTypeS: instanceTypeChost, ServerIP: "10.0.0.1", ServerPort: 80},
		{Method: "GET", Endpoint: "/drop", ObservationPoint: tapServerSide, InstanceTypeS: instanceTypeK8sPod, K8sService: "", K8sNamespace: "", ServerIP: "10.0.0.2", ServerPort: 80},
	}
	out := classifyAndDrop(in)
	if len(out) != 1 {
		t.Fatalf("got %d kept rows, want 1: %+v", len(out), out)
	}
	if out[0].Endpoint != "/keep" {
		t.Errorf("kept wrong row: %+v", out[0])
	}
}
