package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/flow"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

// WatchAdd creates a watch from a description, shows the plan and asks for confirmation.
func WatchAdd(ctx context.Context, description string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Model == "" {
		return fmt.Errorf("no model configured yet; run: scharrel setup")
	}
	dataDir, err := config.DataDir()
	if err != nil {
		return err
	}
	mgr := &llm.Manager{Dir: dataDir, Out: os.Stdout}
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	defer mgr.Stop()

	ask := termAsker{out: os.Stdout}
	draft, err := flow.Interview(ctx, cfg, mgr, description, ask)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(draft.Summary())
	text, examples := flow.Preview(ctx, cfg, draft)
	if text != "" {
		fmt.Println()
		fmt.Println(text)
		for _, e := range examples {
			fmt.Printf("  - %s, %s (%s)\n", e.Title, e.Price, e.URL)
		}
	}

	action := "save"
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Save this search?").Value(&action).Options(
			huh.NewOption("Save", "save"),
			huh.NewOption("Change the description and redraft", "edit"),
			huh.NewOption("Cancel", "cancel"),
		),
	)).Run(); err != nil {
		return err
	}
	switch action {
	case "cancel":
		fmt.Println("Cancelled.")
		return nil
	case "edit":
		edited := description
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Describe what you want").Value(&edited),
		)).Run(); err != nil {
			return err
		}
		return WatchAdd(ctx, edited)
	}
	name := draft.Watch.Name
	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Name for this search").Value(&name),
	)).Run(); err != nil {
		return err
	}
	draft.Watch.Name = strings.TrimSpace(name)
	if err := watch.Save(draft.Watch); err != nil {
		return err
	}
	fmt.Printf("Saved %q. Listings already online are judged 15 per scan and only the best few alert; new ones alert as they appear.\n", draft.Watch.Name)
	return nil
}

// WatchList prints the saved watches.
func WatchList() error {
	watches, err := watch.All()
	if err != nil {
		return err
	}
	if len(watches) == 0 {
		fmt.Println("No searches yet. Add one with: scharrel watch add")
		return nil
	}
	for _, w := range watches {
		state := "paused"
		if w.Enabled {
			state = "active"
		}
		fmt.Printf("%s [%s, %s] %s\n", w.Name, w.Mode, state, strings.Join(w.Queries, ", "))
	}
	return nil
}

// WatchDelete removes a watch after confirmation.
func WatchDelete(name string) error {
	confirm := false
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Delete search %q?", name)).Value(&confirm),
	)).Run(); err != nil {
		return err
	}
	if !confirm {
		return nil
	}
	return watch.Delete(name)
}

// WatchPause switches a watch off or on.
func WatchPause(name string, enabled bool) error {
	return watch.SetEnabled(name, enabled)
}

// UseTemplate saves one of the built-in repair watches.
func UseTemplate(name string) error {
	tpl, ok := watch.Template(name)
	if !ok {
		names := []string{}
		for _, t := range watch.Templates() {
			names = append(names, t.Name)
		}
		return fmt.Errorf("unknown template %q (available: %s)", name, strings.Join(names, ", "))
	}
	if err := watch.Save(tpl); err != nil {
		return err
	}
	fmt.Printf("Added the %q repair search. Edit %s to adjust it.\n", name, "watches/"+name+".yaml")
	return nil
}
