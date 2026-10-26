const main = document.getElementById("main");
const where = document.getElementById("where");
let pollTimer = 0;
let shown = { q: "", card: "" };

const templates = [
  ["audio", "Broken audio gear"],
  ["tv", "Broken TVs"],
  ["computer", "Broken computers"],
];

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text) node.textContent = text;
  return node;
}

function safeURL(raw) {
  try {
    const url = new URL(raw);
    if (url.protocol === "http:" || url.protocol === "https:") return url.href;
  } catch (e) { /* not a link */ }
  return "";
}

async function read(res) {
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "Something went wrong");
  return data;
}

function get(path) { return fetch(path).then(read); }

function post(path, body) {
  return fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  }).then(read);
}

function del(path) { return fetch(path, { method: "DELETE" }).then(read); }

function stopPoll() {
  if (pollTimer) clearTimeout(pollTimer);
  pollTimer = 0;
}

function renderWhere(status) {
  if (!status.configured) {
    where.textContent = "Not set up yet";
    return;
  }
  const bits = [];
  if (status.postcode) bits.push(status.postcode);
  if (status.radius_km) bits.push(status.radius_km + " km");
  if (status.model) bits.push(status.model);
  where.textContent = bits.join(" · ");
}

function showError(message, retry) {
  stopPoll();
  main.replaceChildren();
  main.append(el("p", "qerr", message));
  if (retry) {
    const button = el("button", "secondary", "Back");
    button.addEventListener("click", retry);
    main.append(button);
  }
}

async function load() {
  stopPoll();
  let status;
  try {
    status = await get("/api/status");
  } catch (e) {
    showError("Scharrelaar is not running on this computer.");
    return;
  }
  renderWhere(status);
  if (!status.configured) {
    startSession({ kind: "setup" });
    return;
  }
  showHome(status);
}

async function showHome(status) {
  stopPoll();
  shown = { q: "", card: "" };
  let watches = [];
  try {
    watches = await get("/api/watches");
  } catch (e) {
    showError(e.message, load);
    return;
  }
  main.replaceChildren();
  main.append(renderAlerts(status));
  if (status.configured) main.append(renderWebLookup(status));

  const add = el("form", "panel");
  add.append(el("h2", "", "Find something"));
  const label = el("label", "", "What are you looking for?");
  const area = document.createElement("textarea");
  area.required = true;
  area.placeholder = "A braked car trailer, bed at least 2.5 m, under 900 euro, near Gent";
  label.append(area);
  add.append(label);
	const find = el("button", "", "Find it");
	find.type = "submit";
	const findRow = el("div", "row");
	findRow.append(find);
	add.append(findRow);
  add.addEventListener("submit", (ev) => {
    ev.preventDefault();
    startSession({ kind: "interview", description: area.value });
  });
  main.append(add);

  main.append(el("h2", "", "Your searches"));
  if (watches.length) {
    main.append(el("p", "hint", "Select a search to see what it found, and why it did or did not alert."));
  }
  if (!watches.length) {
    main.append(el("p", "hint", "None yet. Describe something above."));
  }
  for (const w of watches) {
    main.append(renderWatch(w));
  }

  const repairs = el("div", "panel");
  repairs.append(el("h2", "", "Repair searches"));
  repairs.append(el("p", "hint", "Built-in searches for broken equipment worth fixing."));
  const row = el("div", "row");
  for (const [name, labelText] of templates) {
    const button = el("button", "secondary", labelText);
    button.type = "button";
    button.addEventListener("click", async () => {
      button.disabled = true;
      try {
        await post("/api/templates/" + name, {});
        showHome(await get("/api/status"));
      } catch (e) {
        button.disabled = false;
        alert(e.message);
      }
    });
    row.append(button);
  }
  repairs.append(row);
  main.append(repairs);

  const again = el("button", "secondary", "Run setup again");
  again.type = "button";
  again.addEventListener("click", () => startSession({ kind: "setup" }));
	const againRow = el("div", "row");
	againRow.append(again);
	main.append(againRow);
  main.append(el("p", "foot", "This page is only reachable from this computer."));
}

function renderWebLookup(status) {
  const on = !!status.web_lookup;
  const panel = el("div", "panel");
  const heading = el("h2", "", "Look up brands and models ");
  heading.append(el("span", "state " + (on ? "on" : "off"), on ? "On" : "Off"));
  panel.append(heading);
  panel.append(el("p", "hint", on
    ? "When an ad names a brand and model, its specifications are looked up on DuckDuckGo, once per model, so a requirement can be settled from them (a recommended amplifier power shows a speaker is passive). Only the brand and model leave this computer."
    : "Off: listings are judged only on the ad and its photos. Turn it on to look up the specifications of the brand and model an ad names on DuckDuckGo. Only the brand and model leave this computer; nothing about you or your searches."));
  const button = el("button", on ? "secondary" : "", on ? "Turn off" : "Turn on");
  button.type = "button";
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await post(on ? "/api/web-lookup/off" : "/api/web-lookup/on", {});
      panel.replaceWith(renderWebLookup(await get("/api/status")));
    } catch (e) {
      button.disabled = false;
      alert(e.message);
    }
  });
  const row = el("div", "row");
  row.append(button);
  panel.append(row);
  return panel;
}

function renderAlerts(status) {
  const alerts = status.alerts || {};
  const atLogin = status.service === "starts at login";
  const panel = el("div", "panel alerts");
  const heading = el("h2", "", "Alerts ");
  const running = alerts.state === "running";
  heading.append(el("span", "state " + (running ? "on" : "off"), running ? "On" : "Off"));
  panel.append(heading);

  let text = "";
  switch (alerts.state) {
    case "running":
      text = "Scanning in the background. Closing this page does not stop it" +
        (atLogin ? ", and it starts again after a restart." : ".");
      break;
    case "stopped":
      text = "Stopped. No scans and no alerts, also after a restart, until you start them again.";
      break;
    case "paused for setup":
      text = "Paused while setup runs.";
      break;
    case "not configured":
      text = "Finish setup to start alerts.";
      break;
    default:
      text = "Alerts should run but do not.";
  }
  panel.append(el("p", "hint", text));
  if (alerts.problem) panel.append(el("p", "qerr", alerts.problem));
  if (status.service === "not supported") {
    panel.append(el("p", "hint", "This system cannot start Scharrelaar at login; run scharrel ui after a restart."));
  }

  if (alerts.state === "paused for setup" || alerts.state === "not configured") return panel;
  const button = el("button", running ? "secondary" : "", running ? "Stop alerts" : "Start alerts");
  button.type = "button";
  button.addEventListener("click", async () => {
    if (running && !confirm("Stop scanning and alerts? They stay off after a restart until you start them here.")) return;
    button.disabled = true;
    try {
      await post(running ? "/api/alerts/stop" : "/api/alerts/start", {});
      const fresh = await get("/api/status");
      panel.replaceWith(renderAlerts(fresh));
    } catch (e) {
      button.disabled = false;
      alert(e.message);
    }
  });
  const row = el("div", "row");
  row.append(button);
  panel.append(row);
  return panel;
}

function renderWatch(w) {
  const box = el("article", "watch" + (w.enabled ? "" : " paused"));
  const info = el("div");
  const title = el("button", "text", w.name + (w.enabled ? "" : " (paused)"));
  title.type = "button";
  title.addEventListener("click", () => showListings(w.name));
  const heading = el("h3");
  heading.append(title);
  info.append(heading);
  const meta = [w.mode, w.detail].filter(Boolean).join(" · ");
  const extra = w.seen ? " · " + w.seen + " seen" : "";
  info.append(el("p", "meta", meta + extra));
  const actions = el("div", "actions");
  const toggle = el("button", "secondary", w.enabled ? "Pause" : "Resume");
  toggle.type = "button";
  toggle.addEventListener("click", async () => {
    toggle.disabled = true;
    try {
      await post("/api/watches/" + encodeURIComponent(w.name) + "/" + (w.enabled ? "pause" : "resume"), {});
      showHome(await get("/api/status"));
    } catch (e) {
      toggle.disabled = false;
      alert(e.message);
    }
  });
  const remove = el("button", "danger", "Delete");
  remove.type = "button";
  remove.addEventListener("click", async () => {
    if (!confirm("Delete " + w.name + "?")) return;
    try {
      await del("/api/watches/" + encodeURIComponent(w.name));
      showHome(await get("/api/status"));
    } catch (e) {
      alert(e.message);
    }
  });
  actions.append(toggle, remove);
  box.append(info, actions);
  return box;
}

async function showListings(name) {
  stopPoll();
  shown = { q: "", card: "" };
  main.replaceChildren(el("p", "hint", "Loading…"));
  let data;
  try {
    data = await get("/api/watches/" + encodeURIComponent(name) + "/listings");
  } catch (e) {
    showError(e.message, load);
    return;
  }
  renderListings(data, "all");
}

function renderListings(data, filter) {
  main.replaceChildren();
  const back = el("button", "secondary", "All searches");
  back.type = "button";
  back.addEventListener("click", load);
  main.append(back);

  main.append(el("h2", "", data.name || "Search"));
  const bits = [];
  if (data.mode) bits.push(data.mode);
  if (data.detail) bits.push(data.detail);
  if (bits.length) main.append(el("p", "meta", bits.join(" · ")));
  if (data.description) main.append(el("p", "", data.description));
  if (data.must_have && data.must_have.length) {
    main.append(el("p", "hint", "Must have: " + data.must_have.join(", ")));
  }
  for (const check of data.checks || []) {
    main.append(el("p", "hint", "“" + check.wish + "” is checked as: " + check.means));
  }

  const counts = [
    ["all", "All " + (data.total || 0)],
    ["alerted", "Alerted " + (data.alerted || 0)],
    ["judged", "Judged " + (data.judged || 0)],
    ["skipped", "Skipped " + (data.skipped || 0)],
    ["recorded", "Waiting " + (data.recorded || 0)],
  ];
  if (data.errors) counts.push(["errors", "Errors " + data.errors]);
  const bar = el("div", "filters");
  for (const [key, label] of counts) {
    const button = el("button", "secondary", label);
    button.type = "button";
    button.setAttribute("aria-pressed", key === filter ? "true" : "false");
    button.addEventListener("click", () => renderListings(data, key));
    bar.append(button);
  }
  main.append(bar);

  const rows = (data.listings || []).filter((row) => matchesFilter(row, filter));
  if (!data.total) {
    main.append(el("p", "", "Nothing recorded yet. The next scan fills this in. Listings that are already online when you add a search are judged a few at a time, and only the best few of those alert, so you are not flooded."));
    return;
  }
  if (!rows.length) {
    main.append(el("p", "hint", "Nothing in this group."));
    return;
  }
  if (data.shown < data.total) {
    main.append(el("p", "hint", "Showing the " + data.shown + " most relevant of " + data.total + "."));
  }
  for (const row of rows) main.append(renderListing(row));
}

function matchesFilter(row, filter) {
  switch (filter) {
  case "alerted": return row.alerted;
  case "judged": return row.status === "evaluated";
  case "skipped": return row.status === "skipped";
  case "recorded": return row.status === "seeded";
  case "errors": return row.status === "error";
  default: return true;
  }
}

function renderListing(row) {
  const box = el("article", "listing panel");
  const title = el("h3");
  const href = safeURL(row.url);
  if (href) {
    const a = document.createElement("a");
    a.href = href;
    a.target = "_blank";
    a.rel = "noreferrer";
    a.textContent = row.title || row.url;
    title.append(a);
  } else {
    title.textContent = row.title || "Listing";
  }
  box.append(title);
  const meta = [row.price, row.site, row.last_seen].filter(Boolean).join(" · ");
  if (meta) box.append(el("p", "meta", meta));
  const decision = el("p", "decision" + (row.alerted ? " alerted" : ""));
  decision.textContent = row.decision || "";
  box.append(decision);
  if (row.status === "evaluated") {
    const scores = "Deal score " + row.score;
    const learning = row.learning_score ? ", learning score " + row.learning_score : "";
    const photos = row.photos_seen ? " · looked at " + row.photos_seen + (row.photos_seen === 1 ? " photo" : " photos") : "";
    box.append(el("p", "hint", scores + learning + photos));
  }
  if (row.findings && row.findings.length) {
    const list = el("ul", "findings");
    for (const finding of row.findings) list.append(renderFinding(finding));
    box.append(list);
  }
  if (row.seen) {
    box.append(el("p", "hint", "On the photos: " + row.seen));
  }
  if (row.looked_up) {
    box.append(el("p", "hint", "Looked up on the web: " + row.looked_up));
  }
  if (row.questions && row.questions.length) {
    box.append(el("p", "hint", "Still to ask the seller: " + row.questions.join(", ")));
  }
  if (row.notes && row.notes.length) {
    box.append(el("p", "hint", row.notes.join(" · ")));
  }
  if (row.repair) {
    const bits = Object.keys(row.repair).map((key) => key + ": " + row.repair[key]);
    if (bits.length) box.append(el("p", "", bits.join(" · ")));
  }
  return box;
}

function renderFinding(finding) {
  const item = el("li", "finding " + (finding.status || "unknown"));
  const label = { met: "met", unmet: "not met", unknown: "not shown" }[finding.status] || finding.status;
  const source = { photo: " (from the photos)", web: " (from the web)" }[finding.basis] || "";
  const status = el("span", "status", (finding.name || "requirement") + ": " + label + source);
  item.append(status);
  if (finding.basis === "photo" || finding.basis === "web") {
    if (finding.reason) item.append(el("span", "quote", " " + finding.reason));
    if (finding.basis === "web" && finding.quote) item.append(el("span", "quote", " “" + finding.quote + "”"));
  } else if (finding.quote) {
    item.append(document.createTextNode(" "));
    item.append(el("span", "quote", "“" + finding.quote + "”"));
  }
  return item;
}

async function startSession(body) {
  stopPoll();
  shown = { q: "", card: "" };
  main.replaceChildren(el("p", "hint", "Starting…"));
  try {
    const res = await post("/api/sessions", body);
    poll(res.id);
  } catch (e) {
    showError(e.message, load);
  }
}

function poll(id) {
  stopPoll();
  const tick = async () => {
    let state;
    try {
      state = await get("/api/sessions/" + encodeURIComponent(id));
    } catch (e) {
      showError(e.message, load);
      return;
    }
    renderSession(state, id);
    if (!state.done) pollTimer = setTimeout(tick, 700);
  };
  tick();
}

function slotOf(root) {
  let slot = root.querySelector(".slot");
  if (!slot) slot = root.appendChild(el("div", "slot"));
  return slot;
}

function renderSession(state, id) {
  let root = document.getElementById("session");
  if (!root) {
    main.replaceChildren();
    root = el("div");
    root.id = "session";
    main.append(root);
  }
  let log = root.querySelector(".log");
  if (!log) log = root.appendChild(el("pre", "log"));
  log.setAttribute("aria-live", "polite");
  const follow = log.scrollHeight - log.scrollTop - log.clientHeight < 48;
  log.textContent = (state.log || []).join("\n");
  if (follow) log.scrollTop = log.scrollHeight;

  const cardKey = state.card ? (state.card.title || "") + "|" + (state.card.link || "") : "";
  if (cardKey !== shown.card) {
    shown.card = cardKey;
    const old = root.querySelector(".card");
    if (old) old.remove();
    if (state.card) log.after(renderCard(state.card));
  }

  if (state.done) {
    shown.q = "";
    const slot = slotOf(root);
    slot.replaceChildren();
    if (state.kind === "setup" && !state.error) {
      load();
      return;
    }
    if (state.result) slot.append(renderResult(state, id));
    else slot.append(renderStopped(state));
    return;
  }

  const q = state.question;
  if (!q) {
    if (shown.q) {
      shown.q = "";
      slotOf(root).replaceChildren();
    }
    return;
  }
  if (q.id !== shown.q) {
    shown.q = q.id;
    const slot = slotOf(root);
    slot.replaceChildren(renderQuestion(q, id));
  } else {
    const err = root.querySelector(".qerr");
    if (err) err.textContent = q.error || "";
  }
}

function renderCard(card) {
  const box = el("div", "card");
  if (card.title) box.append(el("h2", "", card.title));
  for (const line of card.lines || []) box.append(el("p", "", line));
  if (card.link) {
    const a = document.createElement("a");
    a.textContent = card.link;
    const href = safeURL(card.link);
    if (href) { a.href = href; a.target = "_blank"; a.rel = "noreferrer"; }
    box.append(a);
  }
  if (card.qr_png) {
    const img = document.createElement("img");
    img.alt = "QR code";
    img.src = "data:image/png;base64," + card.qr_png;
    box.append(img);
  }
  return box;
}

function renderQuestion(q, id) {
  const form = el("form", "question");
  form.append(el("h2", "", q.title));
  if (q.description) form.append(el("p", "hint", q.description));
  const err = el("p", "qerr", q.error || "");
  form.append(err);
  if (q.kind === "choose") {
    for (const option of q.options || []) {
      const label = el("label", "option");
      const input = document.createElement("input");
      input.type = q.multiple ? "checkbox" : "radio";
      input.name = "choice";
      input.value = option.value;
      input.checked = !!option.selected;
      label.append(input, document.createTextNode(option.label));
      form.append(label);
    }
  } else {
    const input = document.createElement("input");
    input.name = "text";
    input.value = q.default || "";
    input.type = q.secret ? "password" : "text";
    if (q.secret) input.autocomplete = "off";
    form.append(input);
  }
	const button = el("button", "", "Continue");
	button.type = "submit";
	const buttonRow = el("div", "row");
	buttonRow.append(button);
	form.append(buttonRow);
  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    button.disabled = true;
    const body = { question_id: q.id };
    if (q.kind === "choose") {
      body.values = [...form.querySelectorAll("input:checked")].map((input) => input.value);
    } else {
      body.text = form.querySelector("input").value;
    }
    try {
      await post("/api/sessions/" + id + "/answer", body);
    } catch (e) {
      err.textContent = e.message;
      button.disabled = false;
    }
  });
  return form;
}

function renderResult(state, id) {
  const box = el("div", "result");
  const result = state.result;
  box.append(el("h2", "", "This is the search"));
  box.append(el("pre", "summary", result.summary || ""));
  if (result.preview) box.append(el("p", "", result.preview));
  for (const example of result.examples || []) {
    const p = el("p", "example");
    const a = document.createElement("a");
    a.textContent = example.title + (example.price ? ", " + example.price : "");
    const href = safeURL(example.url);
    if (href) { a.href = href; a.target = "_blank"; a.rel = "noreferrer"; }
    p.append(a);
    box.append(p);
  }
  const nameLabel = el("label", "", "Name for this search");
  const name = document.createElement("input");
  name.type = "text";
  name.value = result.name || "";
	nameLabel.append(name);
	box.append(nameLabel);
	box.append(el("p", "hint", "Lowercase letters, digits and dashes. Saving replaces a search with the same name."));
  const err = el("p", "qerr");
  const row = el("div", "row");
  const save = el("button", "", "Save");
  const redo = el("button", "secondary", "Change the description");
  const cancel = el("button", "secondary", "Cancel");
  save.type = "button";
  redo.type = "button";
  cancel.type = "button";
  row.append(save, redo, cancel);
  box.append(err, row);
  save.addEventListener("click", async () => {
    save.disabled = true;
    try {
      await post("/api/sessions/" + id + "/save", { name: name.value });
      showHome(await get("/api/status"));
    } catch (e) {
      err.textContent = e.message;
      save.disabled = false;
    }
  });
  redo.addEventListener("click", () => showRedraft(result.description || "", id));
  cancel.addEventListener("click", async () => {
    await del("/api/sessions/" + id).catch(() => {});
    showHome(await get("/api/status"));
  });
  return box;
}

function showRedraft(description, id) {
  stopPoll();
  del("/api/sessions/" + id).catch(() => {});
  main.replaceChildren();
  const form = el("form", "panel");
  form.append(el("h2", "", "Change the description"));
  const area = document.createElement("textarea");
  area.value = description;
  area.required = true;
  form.append(area);
	const button = el("button", "", "Try again");
	button.type = "submit";
	const buttonRow = el("div", "row");
	buttonRow.append(button);
	form.append(buttonRow);
  form.addEventListener("submit", (ev) => {
    ev.preventDefault();
    startSession({ kind: "interview", description: area.value });
  });
  main.append(form);
}

function renderStopped(state) {
  const box = el("div", "result");
  box.append(el("h2", "", state.error === "Cancelled." ? "Cancelled" : "Something went wrong"));
  if (state.error) box.append(el("p", "", state.error));
  const back = el("button", "secondary", "Back");
  back.type = "button";
	back.addEventListener("click", load);
	const backRow = el("div", "row");
	backRow.append(back);
	box.append(backRow);
  return box;
}

load();
