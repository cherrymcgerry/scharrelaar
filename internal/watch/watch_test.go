package watch

import (
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)

	w := Watch{Name: "trailer", Enabled: true, Mode: ModeBuy,
		Queries: []string{"aanhangwagen geremd"}, Category: "Auto diversen",
		MaxPrice: 900, MustHave: []string{"braked"}, NiceToHave: []string{"cover included"}}
	if err := Save(w); err != nil {
		t.Fatal(err)
	}
	got, err := Load("trailer")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MustHave) != 1 || got.MaxPrice != 900 || !got.Enabled {
		t.Errorf("got %+v", got)
	}
	reqs := got.Requirements()
	if len(reqs) != 2 || !reqs[0].Hard || reqs[1].Hard {
		t.Errorf("requirements %+v", reqs)
	}
	all, err := All()
	if err != nil || len(all) != 1 {
		t.Fatalf("all: %+v %v", all, err)
	}
	if err := SetEnabled("trailer", false); err != nil {
		t.Fatal(err)
	}
	got, _ = Load("trailer")
	if got.Enabled {
		t.Error("still enabled")
	}
	if err := Delete("trailer"); err != nil {
		t.Fatal(err)
	}
	if all, _ = All(); len(all) != 0 {
		t.Errorf("still %d watches", len(all))
	}
}

func TestValidate(t *testing.T) {
	if err := (Watch{Name: "Bad Name", Mode: ModeBuy, Queries: []string{"x"}}).Validate(); err == nil {
		t.Error("uppercase name accepted")
	}
	if err := (Watch{Name: "ok", Mode: "rent", Queries: []string{"x"}}).Validate(); err == nil {
		t.Error("bad mode accepted")
	}
	if err := (Watch{Name: "ok", Mode: ModeBuy}).Validate(); err == nil {
		t.Error("missing queries accepted")
	}
}

func TestTemplates(t *testing.T) {
	for _, tpl := range Templates() {
		if err := tpl.Validate(); err != nil {
			t.Errorf("template %s: %v", tpl.Name, err)
		}
		if tpl.Mode != ModeRepair || tpl.Prompt == "" {
			t.Errorf("template %s incomplete", tpl.Name)
		}
	}
	if _, ok := Template("audio"); !ok {
		t.Error("audio template missing")
	}
}

func TestTemplateRouting(t *testing.T) {
	audio, _ := Template("audio")
	tv, _ := Template("tv")
	computer, _ := Template("computer")
	cases := []struct {
		w     Watch
		title string
		cat   int
		want  bool
	}{
		{audio, "Marantz 2226 defect", 46, true},
		{audio, "Technics versterker", 41, true},
		{audio, "Samsung tv gaat niet aan", 1120, false},
		{audio, "Canon camera defect", 487, false},
		{audio, "Sony tv met versterker", 46, true}, // title and subcategory both match
		{tv, "LG 55 inch tv standby", 1120, true},
		{tv, "Tv-meubel eik", 504, false},
		{tv, "Philips televisie blijft in standby", 41, true},
		{computer, "Fantastische laptop, gaat niet aan", 339, true},
		{computer, "Laptoptas zwart", 1654, false},
		{computer, "Laptop met tas", 339, true},
		{computer, "HP printer defect", 342, false},
	}
	for _, c := range cases {
		if got := c.w.Belongs(c.title, c.cat); got != c.want {
			t.Errorf("%s: %q in %d = %v, want %v", c.w.Name, c.title, c.cat, got, c.want)
		}
	}
}

func TestBelongsWithoutRouting(t *testing.T) {
	w := Watch{Exclude: []string{"verhuur"}}
	if !w.Belongs("Aanhangwagen geremd", 48) {
		t.Error("plain watch rejected a listing")
	}
	if w.Belongs("Aanhangwagen verhuur", 48) {
		t.Error("exclude word ignored")
	}
}
