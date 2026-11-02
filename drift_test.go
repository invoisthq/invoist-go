package invoist

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/invoisthq/invoist-go/internal/generated"
)

// The hand-written parameter and result structs must name the same query
// parameters and JSON fields as the types generated from openapi.yaml, so
// regenerating after the API gains a parameter fails here until the
// hand-written struct has it too.

func tagNames(t reflect.Type, tag string) []string {
	var names []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get(tag), ",")
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func TestParamsMatchTheSpec(t *testing.T) {
	pairs := []struct {
		ours, spec any
		ourTag     string
		specTag    string
	}{
		{ListDocumentsParams{}, generated.ListDocumentsParams{}, "query", "form"},
		{ListReviewQueueParams{}, generated.ListReviewQueueParams{}, "query", "form"},
		{ListBillsParams{}, generated.ListBillsParams{}, "query", "form"},
		{BillsSummaryParams{}, generated.GetBillsSummaryParams{}, "query", "form"},
		{BillsStatsParams{}, generated.GetBillsStatsParams{}, "query", "form"},
		{CreateWebhookEndpointParams{}, generated.CreateWebhookEndpointJSONBody{}, "json", "json"},
		{ExtractResult{}, generated.ExtractDocument200JSONResponseBody{}, "json", "json"},
		{ExtractResult{}, generated.ExtractDocument201JSONResponseBody{}, "json", "json"},
	}
	for _, p := range pairs {
		ours, spec := reflect.TypeOf(p.ours), reflect.TypeOf(p.spec)
		got, want := tagNames(ours, p.ourTag), tagNames(spec, p.specTag)
		if !slices.Equal(got, want) {
			t.Errorf("%s has %v, the spec's %s has %v", ours.Name(), got, spec.Name(), want)
		}
	}
	// Extract's only parameter is the Idempotency-Key header.
	if n := reflect.TypeFor[generated.ExtractDocumentParams]().NumField(); n != 1 || reflect.TypeFor[ExtractParams]().NumField() != 1 {
		t.Errorf("ExtractDocumentParams has %d fields", n)
	}
}

// Money is int64 everywhere: every *_cents field in every generated type.
func TestMoneyIsInt64(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, where string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for f := range typ.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if strings.HasSuffix(name, "_cents") && ft.Kind() != reflect.Int64 {
				t.Errorf("%s.%s is %s, want int64", where, f.Name, f.Type)
			}
			walk(f.Type, where+"."+f.Name)
		}
	}
	for _, v := range []any{
		Bill{}, BillDetail{}, BillsStats{}, BillsSummary{}, DocumentListItem{}, DocumentDetail{},
		ReviewQueueListItem{}, Usage{}, ExtractResult{}, BatchResult{}, HeldEmail{},
	} {
		walk(reflect.TypeOf(v), reflect.TypeOf(v).Name())
	}
}
