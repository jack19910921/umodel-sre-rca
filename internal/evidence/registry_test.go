package evidence

import (
	"strings"
	"testing"
)

func TestRegistryRejectsURLAsQueryMechanism(t *testing.T) {
	_, err := LoadRegistry("../../testdata/binding-with-query-url.yaml")
	if err == nil || !strings.Contains(err.Error(), "query_url is not supported") {
		t.Fatalf("err=%v", err)
	}
}

func TestRegistryLoadsOnlyKnownSelectors(t *testing.T) {
	registry, err := LoadRegistry("../../testdata/evidence-bindings-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Binding("nginx_access_log"); !ok {
		t.Fatal("nginx_access_log binding not loaded")
	}
}
