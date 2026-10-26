package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/cherrymcgerry/scharrelaar/internal/flow"
	"github.com/skip2/go-qrcode"
)

// termAsker asks flow questions in the terminal.
type termAsker struct {
	out io.Writer
}

func (t termAsker) Progress(msg string) {
	fmt.Fprintln(t.out, msg)
}

func (t termAsker) Show(c flow.Card) {
	if c.Title != "" {
		fmt.Fprintln(t.out, c.Title)
	}
	for _, line := range c.Lines {
		fmt.Fprintln(t.out, line)
	}
	if c.Link != "" {
		fmt.Fprintln(t.out, c.Link)
	}
	if c.QR != "" {
		fmt.Fprintln(t.out, qr(c.QR))
	}
}

func (t termAsker) Choose(q flow.Choice) ([]string, error) {
	if q.Multiple {
		return t.chooseMulti(q)
	}
	var value string
	for _, o := range q.Options {
		if o.Selected {
			value = o.Value
		}
	}
	opts := make([]huh.Option[string], len(q.Options))
	for i, o := range q.Options {
		opts[i] = huh.NewOption(o.Label, o.Value)
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title(q.Title).Description(q.Description).Options(opts...).Value(&value),
	)).Run()
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, fmt.Errorf("pick one")
	}
	return []string{value}, nil
}

func (t termAsker) chooseMulti(q flow.Choice) ([]string, error) {
	var chosen []string
	opts := make([]huh.Option[string], len(q.Options))
	for i, o := range q.Options {
		opts[i] = huh.NewOption(o.Label, o.Value).Selected(o.Selected)
		if o.Selected {
			chosen = append(chosen, o.Value)
		}
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title(q.Title).Description(q.Description).Options(opts...).Value(&chosen).
			Validate(func(v []string) error {
				if q.Min > 0 && len(v) < q.Min {
					return fmt.Errorf("pick at least one")
				}
				return nil
			}),
	)).Run()
	return chosen, err
}

func (t termAsker) Input(q flow.Field) (string, error) {
	value := q.Default
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(q.Title).Description(q.Description).Value(&value).
			Validate(func(s string) error {
				if q.Validate == nil {
					return nil
				}
				if msg := q.Validate(s); msg != "" {
					return fmt.Errorf("%s", msg)
				}
				return nil
			}),
	)).Run()
	return strings.TrimSpace(value), err
}

func qr(content string) string {
	code, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return ""
	}
	return code.ToSmallString(false)
}
