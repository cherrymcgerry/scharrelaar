// Command scharrel watches classifieds sites and sends deals to Telegram.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/cli"
	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/web"
)

// version is set by the linker at release time.
var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "scharrel:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		// A double-click on Windows has no arguments. Open the browser instead of
		// printing usage into a window that closes.
		if runtime.GOOS == "windows" {
			return ui(ctx)
		}
		return usage()
	}
	switch args[0] {
	case "setup":
		if err := cli.Setup(ctx); err != nil {
			return err
		}
		if cfg, err := config.Load(); err == nil && cfg.AlertsOff {
			fmt.Println("Alerts are stopped; scharrel start turns them on.")
			return nil
		}
		return alertsCmd(true)
	case "run":
		return runService(ctx, len(args) > 1 && args[1] == "--background")
	case "ui":
		return ui(ctx)
	case "watch":
		return watchCmd(ctx, args[1:])
	case "status":
		return status()
	case "doctor":
		return cli.Doctor(ctx)
	case "logs":
		return logs(args[1:])
	case "start":
		return alertsCmd(true)
	case "stop":
		return alertsCmd(false)
	case "service":
		return serviceCmd(args[1:])
	case "update":
		return cli.Update(version)
	case "uninstall":
		return uninstall(args[1:])
	case "version", "--version", "-v":
		fmt.Println("scharrel", version)
		return nil
	case "help", "--help", "-h":
		return usage()
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usageText())
	}
}

func usage() error {
	fmt.Println(usageText())
	return nil
}

func usageText() string {
	return `scharrel watches 2dehands.be and marktplaats.nl and alerts you on Telegram.

Usage:
  scharrel setup              configure hardware, model, location and Telegram
  scharrel ui                 open the browser interface, starting it in the background
  scharrel start              turn alerts on; they keep running and start at login
  scharrel stop               turn alerts off, also after a restart
  scharrel run                scan in this window until Ctrl+C (for troubleshooting)
  scharrel watch add          describe what you want, in your own words
  scharrel watch list         show your searches
  scharrel watch pause NAME   stop alerting for a search
  scharrel watch resume NAME  start alerting again
  scharrel watch delete NAME  remove a search
  scharrel watch template NAME  add a built-in repair search (audio, tv, computer)
  scharrel status             show configuration, alert state and searches
  scharrel doctor             check that the marketplaces still respond
  scharrel logs [--model]     show the last 200 log lines (--model: the model server)
  scharrel update             install the newest version
  scharrel uninstall          remove scharrel, including the model
  scharrel version`
}

func watchCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scharrel watch add|list|pause|resume|delete|template")
	}
	switch args[0] {
	case "add":
		description := strings.Join(args[1:], " ")
		if description == "" {
			fmt.Print("What are you looking for? ")
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && err != io.EOF {
				return err
			}
			description = strings.TrimSpace(line)
		}
		if strings.TrimSpace(description) == "" {
			return fmt.Errorf("describe what you want, for example: scharrel watch add braked trailer under 900 euro near Gent")
		}
		return cli.WatchAdd(ctx, description)
	case "list":
		return cli.WatchList()
	case "pause":
		if len(args) < 2 {
			return fmt.Errorf("usage: scharrel watch pause NAME")
		}
		return cli.WatchPause(args[1], false)
	case "resume":
		if len(args) < 2 {
			return fmt.Errorf("usage: scharrel watch resume NAME")
		}
		return cli.WatchPause(args[1], true)
	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: scharrel watch delete NAME")
		}
		return cli.WatchDelete(args[1])
	case "template":
		if len(args) < 2 {
			return fmt.Errorf("usage: scharrel watch template audio|tv|computer")
		}
		return cli.UseTemplate(args[1])
	default:
		return fmt.Errorf("unknown watch command %q", args[0])
	}
}

// serviceCmd keeps the commands of earlier versions working.
func serviceCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scharrel start|stop")
	}
	switch args[0] {
	case "install":
		return alertsCmd(true)
	case "remove":
		return alertsCmd(false)
	default:
		return fmt.Errorf("unknown service command %q; use scharrel start or scharrel stop", args[0])
	}
}

// alertsCmd turns alerts on or off: through the running app when there is one,
// otherwise in the configuration, starting the background process to turn on.
func alertsCmd(on bool) error {
	port := uiPort()
	if probeUI(port) {
		path := "/api/alerts/stop"
		if on {
			path = "/api/alerts/start"
		}
		var state web.AlertState
		if err := postUI(port, path, &state); err != nil {
			return err
		}
		fmt.Println("alerts:", state.State)
		if state.Problem != "" {
			fmt.Println(state.Problem)
		}
		return nil
	}
	if err := config.SetAlerts(on); err != nil {
		return err
	}
	if !on {
		fmt.Println("alerts: stopped")
		return cli.DisableAutostart()
	}
	if err := cli.EnableAutostart(); err != nil {
		fmt.Println("Could not set it to start at login:", err)
	}
	if err := startBackground(port); err != nil {
		return err
	}
	state, _ := runningAlerts(port)
	fmt.Println("alerts:", state.State)
	if state.Problem != "" {
		fmt.Println(state.Problem)
	}
	fmt.Printf("It runs in the background. Open http://127.0.0.1:%d or run scharrel ui.\n", port)
	return nil
}

// runService serves the page and scans until it is interrupted. In the
// background it detaches from any console window, logs only to the file, and
// keeps start at login registered while alerts are on.
func runService(ctx context.Context, background bool) error {
	dataDir, err := config.DataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	logFile, err := openLog(filepath.Join(dataDir, logName))
	if err != nil {
		return err
	}
	defer logFile.Close()
	if background {
		detachConsole()
		log.SetOutput(logFile)
	} else {
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}
	if err := serve(ctx, dataDir, background); err != nil {
		log.Printf("stopped: %v", err)
		return err
	}
	return nil
}

func serve(ctx context.Context, dataDir string, background bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Claim the port before anything else: a second copy, started at login or by
	// a second double-click, must exit rather than scan alongside the first.
	ctx, quit := context.WithCancel(ctx)
	defer quit()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := web.Listen(cfg.UIPort)
	if err != nil {
		return err
	}

	st, err := store.Open(filepath.Join(dataDir, "listings.db"))
	if err != nil {
		ln.Close()
		return err
	}
	defer st.Close()

	mgr := &llm.Manager{Dir: dataDir}
	if mgr.Installed() {
		if err := mgr.Start(ctx); err != nil {
			log.Printf("model server: %v", err)
		}
	}
	defer mgr.Stop()

	sup := newSupervisor(ctx, mgr, st)
	sup.autostart = background
	ui := &web.Server{Port: cfg.UIPort, Version: version, LLM: mgr, Store: st, Engine: sup, Quit: quit}
	ui.Serve(ctx, ln)
	log.Printf("scharrel %s interface on http://127.0.0.1:%d", version, cfg.UIPort)
	sup.ensure()
	<-ctx.Done()
	sup.shutdown()
	log.Print("stopped")
	return nil
}

const logName = "scharrel.log"

// openLog opens the app log for appending, first moving a log over 5 MB aside to
// scharrel.log.1.
func openLog(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > 5<<20 {
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func status() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Printf("scharrel %s\n", version)
	fmt.Printf("model: %s\n", orDefault(cfg.Model, "not configured"))
	fmt.Printf("location: %s %s, radius %d km\n", cfg.Country, cfg.Postcode, cfg.DistanceM/1000)
	fmt.Printf("sites: %s\n", strings.Join(cfg.Sites, ", "))
	fmt.Printf("telegram: %s\n", telegramState(cfg))
	if state, ok := runningAlerts(uiPort()); ok {
		fmt.Printf("alerts: %s\n", state.State)
		if state.Problem != "" {
			fmt.Printf("  %s\n", state.Problem)
		}
	} else if cfg.AlertsOff {
		fmt.Println("alerts: stopped")
	} else {
		fmt.Println("alerts: not running (scharrel start)")
	}
	fmt.Printf("start at login: %s\n", cli.ServiceStatus())
	return cli.WatchList()
}

func telegramState(cfg config.Config) string {
	if cfg.ChatID == "" {
		return "not paired"
	}
	return "paired, chat " + cfg.ChatID
}

// logs prints the end of the app log, or of the model server log with --model.
func logs(args []string) error {
	dataDir, err := config.DataDir()
	if err != nil {
		return err
	}
	name := logName
	if len(args) > 0 && args[0] == "--model" {
		name = "ollama.log"
	}
	data, err := os.ReadFile(filepath.Join(dataDir, name))
	if err != nil {
		return fmt.Errorf("no log yet: %s is written once scharrel has run", name)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	fmt.Println(strings.Join(lines, "\n"))
	return nil
}

func uninstall(args []string) error {
	keepData := len(args) > 0 && args[0] == "--keep-data"
	if err := cli.DisableAutostart(); err != nil {
		fmt.Println("start at login:", err)
	}
	if port := uiPort(); probeUI(port) {
		if err := postUI(port, "/api/quit", nil); err != nil {
			fmt.Println("stopping the running app:", err)
		}
		for deadline := time.Now().Add(15 * time.Second); probeUI(port) && time.Now().Before(deadline); {
			time.Sleep(250 * time.Millisecond)
		}
		// Let it stop the model server and close the database.
		time.Sleep(2 * time.Second)
	}
	if !keepData {
		dataDir, err := config.DataDir()
		if err != nil {
			return err
		}
		configDir, err := config.ConfigDir()
		if err != nil {
			return err
		}
		os.RemoveAll(dataDir)
		os.RemoveAll(configDir)
	}
	fmt.Println("Stopped scharrelaar and removed start at login, the configuration and the model. Delete the scharrel program itself manually.")
	return nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
