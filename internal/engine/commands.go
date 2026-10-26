package engine

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/telegram"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

const helpText = `Commando's:
/list - je zoekopdrachten
/status - wat er gescand is
/pause NAAM - zoekopdracht pauzeren
/resume NAAM - zoekopdracht hervatten

Nieuwe zoekopdrachten maak je in de browser op deze computer (scharrel ui), of met: scharrel watch add`

var verdictReplies = map[string]string{
	store.Relevant:     "Genoteerd: relevant",
	store.NotRelevant:  "Genoteerd: niet relevant. Gelijkaardige advertenties scoren voortaan lager.",
	store.TooExpensive: "Genoteerd: te duur",
}

// listen answers commands and alert buttons from the paired chat until ctx ends.
func (e *Engine) listen(ctx context.Context) {
	offset := 0
	for ctx.Err() == nil {
		updates, err := e.Bot.Poll(ctx, offset, 50)
		if err != nil {
			log.Printf("telegram: %v", err)
			if sleep(ctx, 30*time.Second) != nil {
				return
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.ChatID() != e.ChatID {
				continue
			}
			e.handle(u)
		}
	}
}

func (e *Engine) handle(u telegram.Update) {
	if id := u.CallbackID(); id != "" {
		reply := "Onbekende knop"
		if verdict, watchName, itemID, ok := telegram.ParseFeedback(u.CallbackData()); ok {
			if err := e.Store.AddFeedback(watchName, itemID, verdict); err != nil {
				log.Printf("feedback: %v", err)
				reply = "Opslaan mislukt"
			} else {
				reply = verdictReplies[verdict]
			}
		}
		if err := e.Bot.AnswerCallback(id, reply); err != nil {
			log.Printf("telegram: %v", err)
		}
		return
	}
	text := strings.TrimSpace(u.Text())
	if text == "" {
		return
	}
	if err := e.Bot.Send(e.ChatID, e.command(text), nil, false); err != nil {
		log.Printf("telegram: %v", err)
	}
}

// command runs one text command and returns the reply.
func (e *Engine) command(text string) string {
	fields := strings.Fields(text)
	cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.ToLower(fields[1])
	}
	switch cmd {
	case "/list":
		return listWatches()
	case "/status":
		return e.status()
	case "/pause", "/resume":
		if arg == "" {
			return "Gebruik: " + cmd + " NAAM\n\n" + listWatches()
		}
		enabled := cmd == "/resume"
		if err := watch.SetEnabled(arg, enabled); err != nil {
			return fmt.Sprintf("Zoekopdracht %q niet gevonden.\n\n%s", arg, listWatches())
		}
		if enabled {
			return fmt.Sprintf("%s hervat.", arg)
		}
		return fmt.Sprintf("%s gepauzeerd.", arg)
	default:
		return helpText
	}
}

func listWatches() string {
	watches, err := watch.All()
	if err != nil {
		return "Kon de zoekopdrachten niet lezen: " + err.Error()
	}
	if len(watches) == 0 {
		return "Nog geen zoekopdrachten. Maak er een met: scharrel watch add"
	}
	var b strings.Builder
	for _, w := range watches {
		state := "actief"
		if !w.Enabled {
			state = "gepauzeerd"
		}
		fmt.Fprintf(&b, "%s (%s): %s\n", w.Name, state, strings.Join(w.Queries, ", "))
	}
	return strings.TrimSpace(b.String())
}

func (e *Engine) status() string {
	counts, err := e.Store.Counts()
	if err != nil {
		return "Kon de status niet lezen: " + err.Error()
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	return "Advertenties: " + strings.Join(parts, ", ") + "\n\n" + listWatches()
}
