package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordAndGet(t *testing.T) {
	s := open(t)
	if row, err := s.Get("audio", "m1"); err != nil || row != nil {
		t.Fatalf("missing row returned %+v %v", row, err)
	}
	r := Row{Watch: "audio", ItemID: "m1", Title: "Test", PriceCents: 1500, PriceType: "FIXED", URL: "http://x",
		Site: "2dehands", Status: StatusEvaluated, Score: 8, LearningScore: 6}
	if err := s.Record(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("audio", "m1")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Score != 8 || got.LearningScore != 6 || got.Status != StatusEvaluated {
		t.Errorf("got %+v", got)
	}
	n, err := s.CountForWatch("audio")
	if err != nil || n != 1 {
		t.Errorf("count %d %v", n, err)
	}
	if err := s.MarkNotified("audio", "m1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get("audio", "m1")
	if !got.Notified {
		t.Error("not marked notified")
	}
	counts, err := s.Counts()
	if err != nil || counts[StatusEvaluated] != 1 || counts["notified"] != 1 {
		t.Errorf("counts %v %v", counts, err)
	}
}

func TestWatchesAreIndependent(t *testing.T) {
	s := open(t)
	base := Row{ItemID: "m1", Title: "Aanhangwagen", PriceCents: 1, PriceType: "FIXED", URL: "u"}
	a, b := base, base
	a.Watch, a.Status = "trailer", StatusSkipped
	b.Watch, b.Status = "bakwagen", StatusEvaluated
	if err := s.Record(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(b); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("trailer", "m1"); got == nil || got.Status != StatusSkipped {
		t.Errorf("the second watch overwrote the first: %+v", got)
	}
}

func TestListings(t *testing.T) {
	s := open(t)
	seed := Row{Watch: "trailer", ItemID: "old", Title: "Already there", PriceCents: 10000, PriceType: "FIXED", URL: "http://old", Status: StatusSeeded, Reason: "Already listed when this search was created. Recorded without an alert, and not judged."}
	judged := Row{Watch: "trailer", ItemID: "new", Title: "New one", PriceCents: 80000, PriceType: "FIXED", URL: "http://new", Status: StatusEvaluated, Reason: "Alerted. Deal score 6.", Score: 6,
		EvalJSON: `{"requirements":[{"name":"braked","status":"met","quote":"geremd"}]}`}
	other := Row{Watch: "bike", ItemID: "new", Title: "Other watch", PriceCents: 1, PriceType: "FIXED", URL: "u", Status: StatusSkipped, Reason: "price above max"}
	for _, r := range []Row{seed, judged, other} {
		if err := s.Record(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkNotified("trailer", "new"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Listings("trailer", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ItemID != "new" || !got[0].Notified || got[0].LastSeen == "" {
		t.Fatalf("listings %+v", got)
	}
	activity, err := s.Activity("trailer")
	if err != nil || activity["total"] != 2 || activity["alerted"] != 1 || activity[StatusSeeded] != 1 || activity[StatusEvaluated] != 1 {
		t.Fatalf("activity %+v %v", activity, err)
	}
}

func TestFeedback(t *testing.T) {
	s := open(t)
	if err := s.Record(Row{Watch: "trailer", ItemID: "m1", Title: "Anssems", PriceType: "FIXED", URL: "u", Status: StatusEvaluated}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeedback("trailer", "m1", Relevant); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFeedback("trailer", "m1", NotRelevant); err != nil {
		t.Fatal(err)
	}
	got, err := s.FeedbackFor("trailer", 10)
	if err != nil || len(got) != 1 || got[0].Verdict != NotRelevant || got[0].Title != "Anssems" {
		t.Fatalf("%+v %v", got, err)
	}
}
