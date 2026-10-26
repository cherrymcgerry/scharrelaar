// Package flow turns a description into a saved search, and walks through setup.
// Questions go through an Asker, so the terminal and the browser run the same steps.
package flow

// Option is one selectable answer.
type Option struct {
	Label    string
	Value    string
	Selected bool
}

// Choice is a single- or multi-select question.
type Choice struct {
	Title       string
	Description string
	Options     []Option
	// Multiple allows more than one answer.
	Multiple bool
	// Min is how many answers are required. A single choice always requires one.
	Min int
}

// Field is a text question. Validate returns an error message, or "" when the
// value is acceptable.
type Field struct {
	Title       string
	Description string
	// Default is the prefilled value, which the user can keep.
	Default  string
	Validate func(string) string
	// Secret asks the front end to hide what is typed, for the bot token.
	Secret bool
}

// Card is something to show without waiting for an answer, such as the Telegram
// pairing instructions. QR, when set, is the text the front end renders as a code.
type Card struct {
	Title string
	Lines []string
	Link  string
	QR    string
}

// Asker is how a flow talks to the user.
type Asker interface {
	Progress(msg string)
	Choose(q Choice) ([]string, error)
	Input(q Field) (string, error)
	Show(c Card)
}
