package watch

// Templates are the built-in repair watches, ported from the audio deal bot's
// verticals. They are starting points; users edit them like any other watch.
func Templates() []Watch {
	return []Watch{audioTemplate(), tvTemplate(), computerTemplate()}
}

// Template returns one built-in watch by name.
func Template(name string) (Watch, bool) {
	for _, t := range Templates() {
		if t.Name == name {
			return t, true
		}
	}
	return Watch{}, false
}

const grounding = `Grounding rules (most important):
- Only describe faults the seller actually wrote. Copy their words into fault_quote verbatim.
- If the seller gives no concrete symptom ("herstelling nodig", "defect", "voor onderdelen", "ongetest"), set fault_is_specific=false, fault_category=UNSPECIFIED, perceived_issue="Unspecified fault", likely_cause="unknown", confidence LOW and repair_difficulty MEDIUM. A vague fault is not a reason for AVOID.
- Clear symptoms ARE specific even if the seller adds "geen idee wat er mis is": "gaat niet (meer) aan", "doet niets", "geen teken van leven", "ne s'allume plus", "blijft in standby", "lampje knippert", "geen beeld wel klank", "laadt niet". These are fault_is_specific=true.
- Never assume a symptom the seller did not write.
- fault_category: POWER (dead, no power, standby, blinking LED, clicking relay, fuse), BACKLIGHT (sound but no picture, picture fades after seconds), CHARGING (does not charge, DC jack, charging port), SCREEN_PANEL (cracked/broken screen, lines, stripes, dark patches), LIQUID_DAMAGE, PHYSICAL_DAMAGE (dropped, broken housing), BOOT_OR_SOFTWARE (powers on but won't boot, password, OS), AUDIO_FAULT (sound path problems), OTHER, UNSPECIFIED.

Values:
- estimated_working_value_eur: realistic Belgian/Dutch second-hand price of this model in good working condition. Be conservative.
- estimated_parts_cost_eur: parts only, not labour.
- confidence LOW if the model is unknown or the fault is vague.
Never invent a model that is not in the listing.

Learning value (independent of profit):
- HIGH: a specific symptom with a classic, well-documented diagnosis path on a serviceable device (e.g. dead power supply, protection relay not clicking, one channel dead, backlight fault, no charging).
- MEDIUM: specific but mostly mechanical (belts, cleaning, adjusting), or an unspecified fault on a very serviceable device.
- LOW: unspecified fault, parts-swap only, proprietary parts, or mostly cosmetic.`

func audioTemplate() Watch {
	return Watch{
		Name: "audio", Enabled: true, Mode: ModeRepair,
		Queries:    []string{"defect", "voor onderdelen", "krakend", "herstellen", "geen geluid", "opknapper"},
		NotWorking: true,
		Category:   "Audio, Tv en Foto",
		MaxPrice:   400,
		FaultWords: []string{"krakend", "kraakt", "kraak", "brom", "zoemt", "geen geluid", "valt weg", "slaat af",
			"springt niet", "draait niet"},
		// Bandrecorder, Buizenversterker, Cd-spelers, Stereoketens, Hoofdtelefoons, Luidsprekerboxen,
		// Overige, Platenspelers, Radio's, Tuners, Versterkers, Walkmans, Professionele apparaten,
		// Cassettedecks, Mediaspelers. The ids are the same on 2dehands and Marktplaats.
		Subcategories: []int{32, 33, 35, 36, 37, 38, 41, 42, 43, 45, 46, 47, 1117, 2036, 2668},
		TitleTerms: []string{"versterker", "voorversterker", "eindversterker", "amplifier", "amp", "receiver", "tuner",
			"platenspeler", "draaitafel", "turntable", "cassettedeck", "tapedeck", "cassette deck",
			"bandrecorder", "cd-speler", "cd speler", "cd-player", "compact disc", "luidspreker",
			"speaker", "hifi", "hi-fi", "stereo", "stereoketen",
			"marantz", "denon", "sansui", "technics", "luxman", "revox", "akai", "onkyo", "nad",
			"harman", "kenwood", "pioneer", "yamaha", "thorens", "dual", "bang & olufsen", "b&o"},
		Exclude: []string{"tv", "televisie", "camera", "beamer", "projector", "dvd", "blu-ray", "vhs"},
		Prompt: "You are an experienced vintage hi-fi repair technician who buys broken audio gear on Belgian and Dutch classifieds to repair and resell.\n\n" + grounding + `

Dutch fault vocabulary:
- "krakend", "kraakt", "ruis bij draaien" = scratchy potentiometers/switches (cleaning, EASY)
- "één kanaal", "1 kant", "links/rechts valt weg" = one channel dead (often relay/dirty contacts, sometimes output stage)
- "brom", "zoemt" = hum (filter capacitors, grounding)
- "springt niet aan", "geen stroom" = no power (fuse, power switch, supply)
- "slaat af", "beveiliging", "protect" = protection engaging (DC offset, output transistors, relay)
- "draait niet", "snaar" = belt drive not turning (belt, EASY)
- "lade opent niet" = CD tray (belt/gear, EASY), "leest geen cd" = laser (MEDIUM-HARD)
- "voor onderdelen", "ongetest", "defect" = untested or broken, unknown fault

Difficulty:
- EASY: cleaning pots/switches, fuse, belts, lamps, speaker refoam, stuck relay contacts
- MEDIUM: recapping, relay replacement, cold solder joints, CD laser alignment
- HARD: output transistors, transformer, missing parts, heavily modified units, rare proprietary ICs
- AVOID: cheap plastic micro/mini systems, modern class-D/AV receivers with HDMI board faults, water/fire/drop damage, gutted units

is_quality_device: true for vintage/quality hi-fi worth repairing, false for cheap mass-market gear.`,
	}
}

func tvTemplate() Watch {
	return Watch{
		Name: "tv", Enabled: true, Mode: ModeRepair,
		Queries:       []string{"tv gaat niet aan", "tv standby", "tv defect"},
		NotWorking:    true,
		Category:      "Audio, Tv en Foto",
		MaxPrice:      150,
		Subcategories: []int{1120}, // Televisies
		TitleTerms:    []string{"tv", "televisie", "smart tv", "led tv", "oled", "qled"},
		FaultWords: []string{"gaat niet aan", "gaat niet meer aan", "geen stroom", "doet niets", "standby", "knippert",
			"klikt", "geen beeld", "s'allume", "doet het niet meer"},
		RejectPhrases: []string{"scherm kapot", "kapot scherm", "gebroken scherm", "scherm gebroken", "barst",
			"gebarsten", "breukschade", "scherm defect", "defect scherm", "écran cassé", "ecran cassé",
			"lijnen in beeld", "lijnen op het scherm", "strepen in beeld", "strepen op het scherm", "beeldbuis"},
		AllowedFaults: []string{"POWER", "BACKLIGHT", "UNSPECIFIED"},
		Exclude: []string{"soundbar", "beugel", "muurbeugel", "tv-meubel", "tv meubel", "meubel", "afstandsbediening",
			"decoder", "recorder", "video", "voet", "televisievoet", "standaard", "stand"},
		Prompt: "You are an experienced TV repair technician who buys broken flat-screen TVs, fixes power-supply and backlight faults, and resells them.\n\n" + grounding + `

Dutch/French fault vocabulary:
- "gaat niet (meer) aan", "geen stroom", "doet niets", "ne s'allume plus" = no power (fuse, PSU capacitors, PSU board)
- "rood lampje knippert", "blijft in standby" = stuck in standby (PSU capacitors, sometimes main board)
- "klikt aan en uit", "tikt", "herstart steeds" = relay clicking / boot loop (PSU capacitors, often EASY)
- "geen beeld wel klank/geluid", "zwart beeld met geluid" = backlight (LED strips)
- "beeld valt weg na enkele seconden" = backlight LED strip or backlight driver
- "scherm kapot", "barst", "strepen", "lijnen" = panel damage (SCREEN_PANEL, not worth repairing)

Difficulty:
- EASY: blown fuse, bulging PSU capacitors, swapping a complete PSU board
- MEDIUM: LED backlight strips, backlight driver, main board swap
- HARD: shorted main board SoC, T-con faults, firmware problems
- AVOID: any panel damage, CRT/beeldbuis TVs, water damage, cheap small no-name TVs

is_quality_device: true for a brand-name TV with real resale value (roughly 40 inch or larger, less than ~8 years old), false otherwise.`,
	}
}

func computerTemplate() Watch {
	return Watch{
		Name: "computer", Enabled: true, Mode: ModeRepair,
		Queries:    []string{"laptop defect", "gaat niet aan", "start niet op"},
		NotWorking: true,
		Category:   "Computers en Software",
		MaxPrice:   250,
		// Windows Laptops, Apple Macbooks, Chromebooks, Desktop Pc's, Apple Desktops, Monitoren.
		Subcategories: []int{339, 325, 3020, 328, 324, 336},
		TitleTerms: []string{"laptop", "notebook", "macbook", "imac", "mac mini", "desktop", "pc", "computer",
			"monitor", "chromebook", "thinkpad"},
		FaultWords: []string{"gaat niet aan", "gaat niet meer aan", "doet niets", "geen teken van leven", "laadt niet",
			"laad niet", "stroomaansluiting", "laadpoort", "start niet", "geen beeld"},
		RejectPhrases: []string{"waterschade", "water schade", "vloeistofschade", "icloud lock", "icloud locked",
			"activation lock", "activatieslot", "bios wachtwoord", "bios paswoord", "bios password",
			"zonder moederbord", "geen moederbord", "scherm kapot", "kapot scherm", "gebroken scherm",
			"scherm gebroken", "defect scherm", "scherm defect", "barst", "gebarsten", "écran cassé",
			"koffie over"},
		AllowedFaults: []string{"POWER", "CHARGING", "BACKLIGHT", "UNSPECIFIED"},
		Exclude:       []string{"lader", "oplader", "adapter", "tas", "hoes", "printer", "muis", "toetsenbord", "kabel"},
		Prompt: "You are an experienced laptop, desktop and monitor repair technician who buys broken computers, fixes power and charging faults, and resells them.\n\n" + grounding + `

Dutch fault vocabulary:
- "gaat niet (meer) aan", "doet niets", "geen teken van leven" = no power
- "laadt niet (op)", "stroomaansluiting", "laadpoort" = charging fault / DC jack / charger
- "start niet op", "boot niet", "blauw scherm" = powers on but boot/software problem (BOOT_OR_SOFTWARE)
- "geen beeld" on a device that powers on = backlight or display
- "waterschade", "koffie" = LIQUID_DAMAGE; "icloud/activation lock", "bios wachtwoord" = locked device

Difficulty:
- EASY: desktop PSU swap, faulty charger/cable, CMOS battery, replaceable DC jack on a cable
- MEDIUM: soldered DC jack, bulging capacitors on monitor/motherboard, monitor backlight
- HARD: shorted MOSFET/capacitor hunt on laptop board, charging IC replacement
- AVOID: liquid damage, activation/BIOS lock, GPU/BGA reflow faults, cracked chassis plus screen, very old low-end machines

is_quality_device: true for machines with real resale value (Intel 8th gen / Ryzen 3000 or newer, Apple silicon or recent Intel Macs, business lines like ThinkPad/Latitude/EliteBook, 24"+ quality monitors), false otherwise.`,
	}
}
