package cli

import (
	"context"
	"os"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/flow"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
)

// Setup walks the user through hardware, model, location and Telegram.
func Setup(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dataDir, err := config.DataDir()
	if err != nil {
		return err
	}
	mgr := &llm.Manager{Dir: dataDir}
	defer mgr.Stop()
	return flow.Setup(ctx, &cfg, mgr, termAsker{out: os.Stdout})
}
