// Package watch defines what the user is looking for. A watch is either a "buy"
// search (find me a trailer) or a "repair" search (find broken gear worth fixing).
package watch

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/prefilter"
	"gopkg.in/yaml.v3"
)

// Modes a watch can run in.
const (
	ModeBuy    = "buy"
	ModeRepair = "repair"
)

// Watch is one saved search.
type Watch struct {
	Name    string `yaml:"name"`
	Enabled bool   `yaml:"enabled"`
	Mode    string `yaml:"mode"`
	// Sites limits the search; empty means every enabled site.
	Sites []string `yaml:"sites,omitempty"`
	// Queries are the search phrases, each run separately.
	Queries []string `yaml:"queries"`
	// Category is a top-level category name, matched per site.
	Category string  `yaml:"category,omitempty"`
	MaxPrice float64 `yaml:"max_price_eur,omitempty"`
	RadiusM  int     `yaml:"radius_m,omitempty"`
	Postcode string  `yaml:"postcode,omitempty"`

	// Buy mode. Description is the buyer's own words; every listing is checked
	// against it, so a search phrase that also finds other items does no harm.
	Description string   `yaml:"description,omitempty"`
	MustHave    []string `yaml:"must_have,omitempty"`
	NiceToHave  []string `yaml:"nice_to_have,omitempty"`
	// Checks say what each requirement means and how to recognise it, worked out
	// when the search was made. A requirement without one is checked as written.
	Checks []Check `yaml:"checks,omitempty"`

	// Repair mode.
	FaultWords    []string `yaml:"fault_words,omitempty"`
	RejectPhrases []string `yaml:"reject_phrases,omitempty"`
	// AllowedFaults limits which fault categories score; empty allows all.
	AllowedFaults []string `yaml:"allowed_faults,omitempty"`
	// Prompt is the repair knowledge given to the model.
	Prompt string `yaml:"prompt,omitempty"`
	// NotWorking adds a search for every listing in Category whose condition is
	// "Niet werkend", without a keyword. It finds most broken items.
	NotWorking bool `yaml:"search_not_working,omitempty"`

	// Subcategories and TitleTerms decide which listings belong to this watch. When
	// either is set, a listing must be in one of the subcategories or name one of the
	// terms in its title.
	Subcategories []int    `yaml:"subcategories,omitempty"`
	TitleTerms    []string `yaml:"title_terms,omitempty"`
	// BrowseSubcategories adds a search for every listing in the subcategories,
	// without a keyword, so no wording a seller uses is missed.
	BrowseSubcategories bool `yaml:"browse_subcategories,omitempty"`
	// OnlySubcategories keeps the keyword searches inside the subcategories instead
	// of the whole Category.
	OnlySubcategories bool `yaml:"only_subcategories,omitempty"`
	// Exclude drops listings whose title contains one of these words, unless both
	// the subcategory and a title term match.
	Exclude []string `yaml:"exclude,omitempty"`
	// MinScore overrides the global alert threshold. 0 uses the global one.
	MinScore int `yaml:"min_score,omitempty"`
}

// Requirements lists every requirement with whether it is a hard one.
func (w Watch) Requirements() []Requirement {
	var out []Requirement
	for _, r := range w.MustHave {
		out = append(out, Requirement{Name: r, Hard: true, Check: w.check(r)})
	}
	for _, r := range w.NiceToHave {
		out = append(out, Requirement{Name: r, Hard: false, Check: w.check(r)})
	}
	return out
}

func (w Watch) check(wish string) *Check {
	for i := range w.Checks {
		if strings.EqualFold(strings.TrimSpace(w.Checks[i].Wish), strings.TrimSpace(wish)) {
			return &w.Checks[i]
		}
	}
	return nil
}

// Check is how to recognise one requirement in an ad. Buyers often name a
// property indirectly, by a part or by what it must work with ("speakers with
// red-black wire" means passive speakers); sellers seldom use the buyer's words.
type Check struct {
	// Wish is the requirement as the buyer put it.
	Wish string `yaml:"wish"`
	// Kind is the kind of item the wish asks for, as a seller would name it.
	Kind string `yaml:"kind,omitempty"`
	// Means is the property itself, as a seller or an expert would put it.
	Means string `yaml:"means"`
	// Signs are words in an ad that confirm it; Against are words that rule it out.
	Signs   []string `yaml:"signs,omitempty"`
	Against []string `yaml:"against,omitempty"`
	// Photo is what a photo of a matching item shows, or "" when photos cannot tell.
	Photo string `yaml:"photo,omitempty"`
}

// ItemRequirement names the hard requirement, checked for watches with a
// description, that the listing offers the kind of item described.
const ItemRequirement = "the item described"

// Requirement is one thing the buyer asked for.
type Requirement struct {
	Name  string
	Hard  bool
	Check *Check
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,30}$`)

// Validate checks the watch before it is saved.
func (w Watch) Validate() error {
	if !nameRE.MatchString(w.Name) {
		return errors.New("name must be lowercase letters, digits, dashes or underscores")
	}
	if w.Mode != ModeBuy && w.Mode != ModeRepair {
		return errors.New("mode must be buy or repair")
	}
	browses := w.BrowseSubcategories && len(w.Subcategories) > 0
	if len(w.Queries) == 0 && !w.NotWorking && !browses {
		return errors.New("at least one search query is required")
	}
	if (browses || w.OnlySubcategories) && w.Category == "" {
		return errors.New("searching subcategories needs the category they belong to")
	}
	return nil
}

// Belongs reports whether a listing with this title and subcategory is one this
// watch is about. It is the audio bot's routing: a matching title rescues a wrong
// subcategory, and an exclude word only loses to a listing that matches both.
// Buy watches also accept a title word at the end of a compound ("inbouwvaatwasser").
func (w Watch) Belongs(title string, categoryID int) bool {
	titleMatch := prefilter.HasAnyTerm(title, w.TitleTerms)
	if w.Mode == ModeBuy && !titleMatch {
		titleMatch = prefilter.HasAnyCompoundTerm(title, w.TitleTerms)
	}
	categoryMatch := false
	for _, id := range w.Subcategories {
		if id == categoryID {
			categoryMatch = true
			break
		}
	}
	if prefilter.HasAnyTerm(title, w.Exclude) && !(titleMatch && categoryMatch) {
		return false
	}
	if len(w.TitleTerms) == 0 && len(w.Subcategories) == 0 {
		return true
	}
	return titleMatch || categoryMatch
}

// Dir is the directory the watches are stored in.
func Dir() (string, error) {
	cfg, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "watches"), nil
}

func path(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yaml"), nil
}

// Save writes one watch.
func Save(w Watch) error {
	if err := w.Validate(); err != nil {
		return err
	}
	p, err := path(w.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(w)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Load reads one watch.
func Load(name string) (Watch, error) {
	p, err := path(name)
	if err != nil {
		return Watch{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Watch{}, err
	}
	var w Watch
	if err := yaml.Unmarshal(data, &w); err != nil {
		return Watch{}, err
	}
	return w, nil
}

// All reads every saved watch.
func All() ([]Watch, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Watch
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		w, err := Load(strings.TrimSuffix(e.Name(), ".yaml"))
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// Delete removes a watch.
func Delete(name string) error {
	p, err := path(name)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// SetEnabled flips a watch on or off.
func SetEnabled(name string, enabled bool) error {
	w, err := Load(name)
	if err != nil {
		return err
	}
	w.Enabled = enabled
	return Save(w)
}
