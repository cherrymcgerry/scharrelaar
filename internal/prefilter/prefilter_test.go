package prefilter

import "testing"

func rules() Rules {
	return Rules{
		MaxPriceEUR: 400, RejectPhrases: []string{"scherm kapot"},
		FaultWords: DefaultFaultWords, RequireFault: true, ConditionFault: "Niet werkend",
		WantedTerms: DefaultWanted, NegativePhrases: DefaultNegatives,
	}
}

func TestHasCompoundTerm(t *testing.T) {
	for _, c := range []struct {
		text, term string
		want       bool
	}{
		{"Bosch inbouwvaatwasser", "vaatwasser", true},
		{"inbouwvaatwassers te koop", "vaatwasser", true},
		{"Vaatwasser", "vaatwasser", true},
		{"vaatwassertje", "vaatwasser", false},
		{"Samsung smarttv", "tv", false},
	} {
		if got := HasCompoundTerm(c.text, c.term); got != c.want {
			t.Errorf("HasCompoundTerm(%q, %q) = %v", c.text, c.term, got)
		}
	}
}

func TestHasTerm(t *testing.T) {
	cases := []struct {
		text, term string
		want       bool
	}{
		{"Fantastische laptop", "tas", false},
		{"Twee laptoptassen", "laptoptas", false},
		{"Speakers te koop", "speaker", true},
		{"Laptop + tas", "tas", true},
		{"Twee tv's", "tv", true},
		{"tvbox android", "tv", false},
		{"Pioneer amplifier", "amp", false},
		{"Oude amp, kraakt", "amp", true},
		{"BANG & OLUFSEN beovox", "bang & olufsen", true},
		{"Écran cassé", "écran", true},
		{"standby lampje", "stand", false},
		{"", "tv", false},
	}
	for _, c := range cases {
		if got := HasTerm(c.text, c.term); got != c.want {
			t.Errorf("HasTerm(%q, %q) = %v, want %v", c.text, c.term, got, c.want)
		}
	}
}

func TestReason(t *testing.T) {
	r := rules()
	cases := []struct {
		l    Listing
		want string
	}{
		{Listing{Title: "Defecte laptops gezocht", Description: "ik koop alles", PriceType: "FREE"}, "wanted ad"},
		{Listing{Title: "Versterker", PriceType: "RESERVED"}, "reserved"},
		{Listing{Title: "Versterker", PriceType: "FIXED", PriceEUR: 900, Condition: "Niet werkend"}, "price above max"},
		{Listing{Title: "TV", Description: "scherm kapot helaas", PriceType: "FIXED", PriceEUR: 50, Condition: "Niet werkend"}, "says 'scherm kapot'"},
		{Listing{Title: "Versterker", Description: "in perfecte staat", PriceType: "FIXED", PriceEUR: 100}, "no fault mentioned"},
		{Listing{Title: "Versterker", Description: "geen defect, alles werkt", PriceType: "FIXED", PriceEUR: 100}, "no fault mentioned"},
		{Listing{Title: "Versterker", Description: "krakend, defect", PriceType: "FIXED", PriceEUR: 100}, ""},
		{Listing{Title: "Versterker", PriceType: "FIXED", PriceEUR: 100, Condition: "Niet werkend"}, ""},
	}
	for _, c := range cases {
		if got := Reason(c.l, r); got != c.want {
			t.Errorf("%q: got %q, want %q", c.l.Title, got, c.want)
		}
	}
}
