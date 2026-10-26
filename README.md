# Scharrelaar

Scharrelaar watches [2dehands.be](https://www.2dehands.be) and [Marktplaats.nl](https://www.marktplaats.nl) for the things you are looking for, and messages you on Telegram when a good one appears. It runs a language model on your own computer, so your searches stay on your machine and you pay nothing per lookup.

You describe what you want in plain language. "A braked car trailer with a bed of at least 2.5 m, under 900 euro, within 60 km of Gent" becomes a search the app checks every few minutes. It also ships ready-made searches for repairing broken audio gear, TVs and computers.

## What you need

- Windows 10 or 11 (64-bit), or Linux on amd64 or arm64.
- Preferably an NVIDIA or AMD graphics card with at least 6 GB of memory; 8 GB or more gives noticeably better judgements. It also runs on the processor alone, slowly.
- About 15 GB of free disk space for the models.
- A Telegram account, for the alerts.

Everything else, including the model server, is downloaded by the installer.

## Install

One command. On Linux:

```sh
curl -fsSL https://github.com/cherrymcgerry/scharrelaar/releases/latest/download/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://github.com/cherrymcgerry/scharrelaar/releases/latest/download/install.ps1 | iex
```

The installer downloads a single program, puts it on your PATH and starts the setup. Setup asks four things:

1. **Hardware.** It looks for an NVIDIA or AMD graphics card and suggests a model that fits your memory. A 12 GB card gets the best model; smaller cards get a smaller one. Without a usable card it falls back to the processor and warns you that it will be slow.
2. **The model.** It downloads its own copy of the model server and the model, then checks that the model answers correctly. This is the slow part, a few gigabytes. It also offers a second model that looks at the photos of each listing (6 GB, needs a graphics card).
3. **Where you search.** Your country, postcode and how far you will travel.
4. **Telegram.** It shows you how to create a bot with @BotFather, checks the token you paste, and shows a code you tap on your phone to link the chat. Only that chat can send commands.

When setup finishes, the alerts start. They run in the background: closing the terminal or the page does not stop them, and they start again when you log in (on Linux at boot, where the system allows it). Stop and start them from the page, or with:

```sh
scharrel stop
scharrel start
```

Stopped stays stopped, also after a restart, until you start the alerts again.

## Use it in your browser

The background process serves a page on this computer only, at <http://127.0.0.1:8765>. `scharrel ui` starts the background process if it is not running and opens the page. On Windows, double-clicking the program does the same.

From that page you can:

- finish setup, including creating the Telegram bot and pairing your phone;
- describe what you want and answer the same questions the terminal asks (where it is listed, your budget, what it must have);
- pause, resume and delete searches, and add a built-in repair search;
- open a search to see every listing it found, what was made of your requirements, and why it did or did not alert;
- stop and start the alerts.

The page is not reachable from another computer. The port can be changed with `ui_port` in the configuration file.

## Find something

Describe it:

```sh
scharrel watch add a braked car trailer, bed at least 2.5 m, max 900 euro, within 60 km of Gent
```

The app then interviews you, using what is actually listed to ask sensible questions:

- It asks the marketplace where listings for your item end up and lets you pick the subcategories (for a trailer: "Aanhangers en Bagagewagens", not the toy trucks or the parts).
- When a subcategory is mostly your item, it simply watches every listing in it, so you don't miss the seller who wrote "remorque" or "bagagewagen". Otherwise it searches with the names sellers use, found in nearby titles (you can untick any).
- It asks for a budget, showing what the item usually costs nearby, and for a postcode when you only gave a town.
- It asks what the item must be or have, offering the kinds and features sellers mention (closed, braked, tandem axle, ...). The ones you already said are ticked.
- It works out what each requirement means, because sellers rarely use your words. "Speakers met rood-zwarte draad" becomes passive speakers with clamp terminals for speaker cable, ruled out by "actief", Bluetooth or a built-in amplifier. It reads how sellers on the site use your words to get there, and offers its best readings for you to pick from, or lets you explain in your own words.

You then see the plan and a few listings that match right now, so you can check it before saving. Say yes, change the description, or cancel.

From then on, new matching listings arrive in Telegram with a score, which of your requirements the listing meets, and what is still unclear so you know what to ask the seller. A requirement only counts as met or not met with evidence: the seller's own words, or what the photos show. The photo model first describes the photos without knowing what you are looking for, and the judgement must quote that description, so it cannot "see" what it was told to look for. Whether a requirement is met is not guessed from the brand and model: small local models confidently get that wrong. Buttons mark a listing as relevant, not relevant or too expensive; listings that resemble ones you marked not relevant score lower from then on. Listings that were already online when you created the search are judged too, 15 per scan, and only the best 3 of each batch alert so you are not flooded on the first run. The rest are shown with their scores in the browser interface.

### Looking up brands and models

What the seller does not say can often be found online: a speaker's specifications show whether it needs an amplifier. Turn on "Look up brands and models" in the browser interface and Scharrelaar searches DuckDuckGo for the brand and model an ad names, and a requirement may then also count as met or not met by what those pages say, quoted like the ad text. It is off by default. Only the brand and model leave your computer, never your searches or requirements, and only when both appear in the ad itself. Results are kept for 90 days, so each model is looked up once, and searches are spaced a few seconds apart. DuckDuckGo may block automated searches now and then; Scharrelaar then stops looking things up until the next scan and judges as if the switch were off.

In the Telegram chat you can also send `/list`, `/status`, `/pause NAME` and `/resume NAME`.

Or start from a built-in repair search:

```sh
scharrel watch template audio
scharrel watch template tv
scharrel watch template computer
```

These look for broken equipment that is worth repairing: every listing marked "Niet werkend" in the category, plus a few keyword searches. They score two ways: as a deal (is there money in it) and as a learning project (is it a good, cheap thing to practise on). You get an alert when either score is high enough.

Manage them with `scharrel watch list`, `scharrel watch pause NAME`, `scharrel watch resume NAME` and `scharrel watch delete NAME`.

## Commands

| Command | What it does |
| --- | --- |
| `scharrel setup` | Run the setup again |
| `scharrel ui` | Open the browser interface, starting it in the background if needed |
| `scharrel start` | Turn alerts on; they run in the background and start at login |
| `scharrel stop` | Turn alerts off, also after a restart |
| `scharrel run` | Scan in the terminal until Ctrl+C, for troubleshooting |
| `scharrel status` | Show configuration, alert state and searches |
| `scharrel doctor` | Check that the marketplaces still answer |
| `scharrel logs` | Show the last 200 lines of the log (`--model` for the model server's) |
| `scharrel update` | Install the newest version |
| `scharrel uninstall` | Stop it and remove start at login, the configuration and the model |

## Where your files are

On Linux, configuration is in `~/.config/scharrelaar/` and data (the database and the model) in `~/.local/share/scharrelaar/`. On Windows both are under `%APPDATA%\scharrelaar\`. Searches are YAML files in the `watches` folder, so you can edit or share them. The log is `scharrel.log` in the data folder. On Windows the Telegram token is stored in Credential Manager; on Linux it is in `~/.config/scharrelaar/token`, readable only by you, because the background service usually cannot unlock the desktop keyring.

## A note on the marketplaces

2dehands and Marktplaats do not offer a public API for this. Scharrelaar reads the same pages you would, slowly: at least three minutes between scans, one request at a time, with a clear user agent. It is meant for your own use, looking for things you intend to buy. Please don't point it at the sites faster than that, and read each site's terms of service before you rely on it. If a site changes its pages, `scharrel doctor` reports it.

## Building from source

You need Go 1.27 or newer.

```sh
go test ./...
go build -o scharrel ./cmd/scharrel
```

Releases are built with [GoReleaser](https://goreleaser.com) for Linux (amd64 and arm64) and Windows (amd64). The configuration is in `.goreleaser.yaml`.

## Problems and ideas

Open an [issue](https://github.com/cherrymcgerry/scharrelaar/issues). For a listing that was judged wrongly, include the link to the ad, what you searched for, and the output of `scharrel status`. Pull requests are welcome; run `go vet ./...` and `go test ./...` first.

## Licence

MIT. See [LICENSE](LICENSE).
