package score

import "testing"

func TestRepairGates(t *testing.T) {
	p := DefaultRepair()
	l := ListingFacts{PriceEUR: 10, PriceType: "FIXED"}

	noFault := Eval{HasFault: false, WorkingValueEUR: 200, Difficulty: Easy}
	if d := Repair(l, noFault, p); d.Score != 1 {
		t.Errorf("no fault scored %d", d.Score)
	}

	outOfScope := Eval{HasFault: true, FaultCategory: "SCREEN_PANEL", Difficulty: Easy, WorkingValueEUR: 200}
	p2 := p
	p2.AllowedFaults = map[string]bool{"POWER": true}
	if d := Repair(l, outOfScope, p2); d.Score != 1 {
		t.Errorf("out of scope scored %d", d.Score)
	}

	cheap := Eval{HasFault: true, FaultCategory: "POWER", Difficulty: Easy, Confidence: High,
		IsQualityDevice: true, WorkingValueEUR: 45, PartsCostEUR: 5}
	if d := Repair(l, cheap, p); d.Score != 2 || d.ProfitEUR != 30 {
		t.Errorf("below min profit: score %d profit %.0f", d.Score, d.ProfitEUR)
	}
}

func TestRepairScore(t *testing.T) {
	p := DefaultRepair()
	l := ListingFacts{PriceEUR: 40, PriceType: "FIXED"}
	ev := Eval{HasFault: true, FaultCategory: "AUDIO_FAULT", Difficulty: Easy, Confidence: High,
		IsQualityDevice: true, WorkingValueEUR: 200, PartsCostEUR: 20}
	d := Repair(l, ev, p)
	if d.ProfitEUR != 140 {
		t.Errorf("profit %.0f", d.ProfitEUR)
	}
	if d.Score < 7 {
		t.Errorf("expected a high score, got %d", d.Score)
	}
	ev.IsQualityDevice = false
	low := Repair(l, ev, p)
	if low.Score >= d.Score {
		t.Errorf("non-quality %d not below quality %d", low.Score, d.Score)
	}
}

func TestBidEstimate(t *testing.T) {
	p := DefaultRepair()
	l := ListingFacts{PriceType: "FAST_BID", HighestBid: 30}
	ev := Eval{HasFault: true, Difficulty: Medium, Confidence: Medium, IsQualityDevice: true,
		WorkingValueEUR: 80, PartsCostEUR: 10}
	d := Repair(l, ev, p)
	if !d.BuyIsEstimate || d.BuyEUR != 33 {
		t.Errorf("buy %.0f estimate %v", d.BuyEUR, d.BuyIsEstimate)
	}
}

func TestLearningScore(t *testing.T) {
	p := DefaultRepair()
	l := ListingFacts{PriceEUR: 15, PriceType: "MIN_BID"}
	ev := Eval{HasFault: true, FaultIsSpecific: true, FaultCategory: "POWER", Difficulty: Medium,
		Confidence: Medium, LearningValue: Medium, WorkingValueEUR: 40, PartsCostEUR: 5}
	d := Repair(l, ev, p)
	if d.Score != 2 {
		t.Errorf("deal score %d, want 2 (profit too low)", d.Score)
	}
	if d.LearningScore < 7 {
		t.Errorf("learning score %d, want >= 7", d.LearningScore)
	}

	ev.FaultIsSpecific = false
	ev.LearningValue = Low
	vague := Repair(l, ev, p)
	if vague.LearningScore >= 7 {
		t.Errorf("vague fault learning score %d, want < 7", vague.LearningScore)
	}
}

func TestGroundRepair(t *testing.T) {
	ev := Eval{FaultQuote: "springt niet aan", FaultIsSpecific: true, FaultCategory: "POWER",
		Difficulty: Easy, Confidence: High, LearningValue: High, IsQualityDevice: true}
	GroundRepair(&ev, "Herstelling nodig. Prijs overeen te komen.")
	if ev.FaultQuote != "" || ev.FaultIsSpecific || ev.FaultCategory != Unspec {
		t.Errorf("quote not discarded: %+v", ev)
	}
	if ev.Difficulty != Medium || ev.LearningValue != Medium || ev.SkillLearned != "unknown" {
		t.Errorf("grounding incomplete: %+v", ev)
	}

	ev2 := Eval{FaultQuote: "De tweeter geeft geen geluid.", FaultIsSpecific: true, FaultCategory: "AUDIO_FAULT",
		Difficulty: Medium, Confidence: Medium}
	GroundRepair(&ev2, "Bose 301, defect. De tweeter geeft geen geluid!")
	if ev2.FaultQuote == "" || ev2.FaultCategory != "AUDIO_FAULT" {
		t.Errorf("real quote discarded: %+v", ev2)
	}
}

func TestBuyScore(t *testing.T) {
	l := ListingFacts{PriceEUR: 650, PriceType: "FIXED"}
	ev := Eval{Requirements: []Requirement{
		{Name: "braked", Status: "met", Quote: "geremd, 750 kg"},
		{Name: "bed length >= 2.5 m", Status: "unknown"},
	}}
	hard := []bool{true, false}
	d := Buy(l, ev, BuyParams{Hard: hard, MedianEUR: 780})
	if d.Score < 6 {
		t.Errorf("score %d, want >= 6", d.Score)
	}
	if len(d.Questions) != 1 || d.RequirementsMet != 1 {
		t.Errorf("met %d questions %v", d.RequirementsMet, d.Questions)
	}

	ev.Requirements[0].Status = "unmet"
	rejected := Buy(l, ev, BuyParams{Hard: hard, MedianEUR: 780})
	if rejected.Score != 1 {
		t.Errorf("unmet hard requirement scored %d", rejected.Score)
	}

	ev.Requirements[0].Status = "unmet"
	soft := Buy(l, ev, BuyParams{Hard: []bool{false, false}, MedianEUR: 780})
	if soft.Score == 1 {
		t.Error("unmet nice-to-have rejected the listing")
	}
}

func TestBuyUnknownStillAlerts(t *testing.T) {
	l := ListingFacts{PriceEUR: 700, PriceType: "FIXED"}
	ev := Eval{Requirements: []Requirement{{Name: "braked", Status: "unknown"}}}
	d := Buy(l, ev, BuyParams{Hard: []bool{true}, MedianEUR: 750})
	if d.Score < 4 {
		t.Errorf("unknown requirement scored %d; it should alert with a question", d.Score)
	}
	confirmed := Buy(l, Eval{Requirements: []Requirement{{Name: "braked", Status: "met", Quote: "geremd"}}}, BuyParams{Hard: []bool{true}, MedianEUR: 750})
	if confirmed.Score <= d.Score {
		t.Errorf("confirmed %d not above unknown %d", confirmed.Score, d.Score)
	}
}

func TestBuyWithoutRequirements(t *testing.T) {
	d := Buy(ListingFacts{PriceEUR: 300, PriceType: "FIXED"}, Eval{}, BuyParams{MedianEUR: 400})
	if d.Score < 6 {
		t.Errorf("a listing for a watch without requirements scored %d", d.Score)
	}
}

func TestGroundBuy(t *testing.T) {
	ev := Eval{Requirements: []Requirement{{Name: "braked", Status: "met", Quote: "geremd tot 750 kg"}}}
	GroundBuy(&ev, "Mooie aanhangwagen, weinig gebruikt.", nil)
	if ev.Requirements[0].Status != "unknown" {
		t.Errorf("ungrounded requirement kept: %+v", ev.Requirements[0])
	}

	text := "Prachtige passieve luidsprekers Kef iq3, weinig gebruikt."
	ev = Eval{Requirements: []Requirement{
		{Name: "passive", Status: "met", Basis: BasisText, Quote: "passieve luidsprekers"},
		{Name: "terminals", Status: "unmet", Basis: BasisText, Quote: "rood-zwarte draad"},
		{Name: "terminals", Status: "met", Basis: BasisPhoto, Quote: "red and black clips", Reason: "clips for speaker cable"},
	}}
	GroundBuy(&ev, text, nil)
	if ev.Requirements[0].Status != "met" {
		t.Errorf("quoted verdict was dropped: %+v", ev.Requirements[0])
	}
	if ev.Requirements[1].Status != "unknown" {
		t.Errorf("unmet verdict without the seller's words was kept: %+v", ev.Requirements[1])
	}
	if ev.Requirements[2].Status != "unknown" {
		t.Errorf("photo verdict kept without photos: %+v", ev.Requirements[2])
	}
	sources := map[string]string{
		BasisPhoto: "A wooden speaker. On the back: red and black clips and a bass port.",
		BasisWeb:   "- KEF iQ3 Specification: Type: 2 way. Recommended Amplifier: 15 to 120W.",
	}
	ev.Requirements[2] = Requirement{Name: "terminals", Status: "met", Basis: BasisPhoto, Quote: "red and black clips", Reason: "clips for speaker cable"}
	GroundBuy(&ev, text, sources)
	if ev.Requirements[2].Status != "met" {
		t.Errorf("photo verdict dropped: %+v", ev.Requirements[2])
	}
	ev.Requirements[2] = Requirement{Name: "terminals", Status: "met", Basis: BasisPhoto, Quote: "red and black wire terminals", Reason: "terminals"}
	GroundBuy(&ev, text, sources)
	if ev.Requirements[2].Status != "unknown" {
		t.Errorf("photo verdict kept for something the photos do not show: %+v", ev.Requirements[2])
	}

	ev = Eval{Requirements: []Requirement{
		{Name: "passive", Status: "met", Basis: BasisWeb, Quote: "Recommended Amplifier: 15 to 120W", Reason: "needs an amplifier, so passive"},
		{Name: "passive", Status: "met", Basis: BasisWeb, Quote: "passive speaker", Reason: "passive"},
		{Name: "passive", Status: "met", Basis: BasisWeb, Quote: "Recommended Amplifier", Reason: ""},
	}}
	GroundBuy(&ev, text, sources)
	if ev.Requirements[0].Status != "met" {
		t.Errorf("web verdict quoting the results was dropped: %+v", ev.Requirements[0])
	}
	if ev.Requirements[1].Status != "unknown" || ev.Requirements[2].Status != "unknown" {
		t.Errorf("web verdict without a quote from the results, or without a reason, was kept: %+v", ev.Requirements)
	}
}

func TestMedian(t *testing.T) {
	if got := Median(nil); got != 0 {
		t.Errorf("empty %.0f", got)
	}
	if got := Median([]float64{900, 650, 780}); got != 780 {
		t.Errorf("got %.0f", got)
	}
	if got := Median([]float64{100, 300}); got != 200 {
		t.Errorf("got %.0f", got)
	}
}
