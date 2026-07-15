package extract

import "testing"

func fact(id, origin, certainty string) Fact {
	return Fact{Kind: "route", ID: id, Origin: origin, Certainty: certainty}
}

func TestMergePriority(t *testing.T) {
	sitterFacts := []Fact{fact("route.get./a", OriginSitter, CertaintyStatic)}
	extractorFacts := []Fact{fact("route.get./a", "php", CertaintyStatic)}
	provedFacts := []Fact{fact("route.get./a", "php", CertaintyProved)}

	merged := Merge(sitterFacts, extractorFacts)
	if len(merged) != 1 || merged[0].Origin != "php" {
		t.Fatalf("extractor should beat sitter, got %+v", merged)
	}

	merged = Merge(provedFacts, extractorFacts)
	if len(merged) != 1 || merged[0].Certainty != CertaintyProved {
		t.Fatalf("proved should beat static regardless of order, got %+v", merged)
	}

	merged = Merge(extractorFacts, provedFacts)
	if len(merged) != 1 || merged[0].Certainty != CertaintyProved {
		t.Fatalf("proved should override an earlier static fact, got %+v", merged)
	}
}

func TestMergeSortsAndKeepsDistinctIDs(t *testing.T) {
	merged := Merge([]Fact{fact("b", OriginSitter, CertaintyStatic)}, []Fact{fact("a", OriginSitter, CertaintyStatic)})
	if len(merged) != 2 || merged[0].ID != "a" || merged[1].ID != "b" {
		t.Fatalf("merge should sort by id, got %+v", merged)
	}
}

func TestDomainOf(t *testing.T) {
	cases := []struct {
		namespace string
		path      string
		want      string
	}{
		{`App\Services\Billing`, "app/Services/Billing/X.php", "services"},
		{`App\Models`, "app/Models/Invoice.php", "models"},
		{"billing", "internal/billing/calc.go", "billing"},
		{"", "routes/web.php", "routes"},
		{"", "web.php", "root"},
		{"crate::billing::rules", "src/billing/rules.rs", "billing"},
	}
	for _, c := range cases {
		f := Fact{Attrs: map[string]string{}}
		if c.namespace != "" {
			f.Attrs["namespace"] = c.namespace
		}
		if got := DomainOf(f, c.path); got != c.want {
			t.Errorf("DomainOf(ns=%q, path=%q) = %q, want %q", c.namespace, c.path, got, c.want)
		}
	}
}

func TestDomainDepthSplitsSingleRoot(t *testing.T) {
	ns := `AP\Core\Controller\Sdk`
	cases := map[int]string{
		1: "core",
		2: "core-controller",
		3: "core-controller-sdk",
		9: "core-controller-sdk", // capped at available segments
	}
	for depth, want := range cases {
		got := DomainResolver{Strategy: "auto", Depth: depth}.Resolve(ns, "src/x.php")
		if got != want {
			t.Errorf("depth %d = %q, want %q", depth, got, want)
		}
	}
	// depth 0 behaves like depth 1 (backward compatible).
	if got := (DomainResolver{Strategy: "auto"}).Resolve(ns, "src/x.php"); got != "core" {
		t.Errorf("depth 0 = %q, want core", got)
	}
	// short namespace: depth caps gracefully.
	if got := (DomainResolver{Strategy: "auto", Depth: 3}).Resolve(`AP\Core`, "x.php"); got != "core" {
		t.Errorf("short ns = %q, want core", got)
	}
}

func TestDomainResolverStrategies(t *testing.T) {
	directory := DomainResolver{Strategy: "directory"}
	if got := directory.Resolve(`App\Services\Billing`, "app/Services/X.php"); got != "app" {
		t.Errorf("directory strategy = %q, want app", got)
	}
	namespace := DomainResolver{Strategy: "namespace"}
	if got := namespace.Resolve("", "app/Services/X.php"); got != "app" {
		t.Errorf("namespace strategy without namespace should fall back to path, got %q", got)
	}
}

func TestParseLines(t *testing.T) {
	a, b, err := ParseLines("12-87")
	if err != nil || a != 12 || b != 87 {
		t.Fatalf("ParseLines(12-87) = %d,%d,%v", a, b, err)
	}
	for _, invalid := range []string{"", "12", "12-", "0-5", "9-3", "a-b", "1-2x"} {
		if _, _, err := ParseLines(invalid); err == nil {
			t.Errorf("ParseLines(%q) should fail", invalid)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"POST /api/activate":  "post-api-activate",
		"App Services":        "app-services",
		"éé--weird__ chars!!": "weird-chars",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
