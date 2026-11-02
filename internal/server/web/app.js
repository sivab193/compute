/* compute.siv19.dev dashboard client.
 *
 * Every selectable option in the submit form is built from /api/capabilities,
 * which the control plane derives from live heartbeats, so the operator is only
 * ever offered something an online machine can actually satisfy. The combined
 * selection is checked against the real scheduler via /api/feasibility before
 * submitting. */

const TOKEN = localStorage.getItem("poolToken") || "";
const AUTH = TOKEN ? { Authorization: "Bearer " + TOKEN } : {};
const QTOK = TOKEN ? "?token=" + encodeURIComponent(TOKEN) : "";

const $ = (id) => document.getElementById(id);

/* ---------- formatting ---------- */

// Format megabytes with a separate unit span, so the display face keeps the
// figure and the unit stays in the body face.
function mbParts(v) {
  if (!v) return ["0", "MB"];
  if (v >= 1024 * 1024) return [(v / 1048576).toFixed(v >= 10485760 ? 0 : 1), "TB"];
  if (v >= 1024) return [(v / 1024).toFixed(v >= 10240 ? 0 : 1), "GB"];
  return [String(Math.round(v)), "MB"];
}
function mbText(v) {
  const [n, u] = mbParts(v);
  return n + " " + u;
}
function figure(el, value, unit) {
  el.innerHTML = "";
  el.append(document.createTextNode(value));
  if (unit) {
    const u = document.createElement("span");
    u.className = "u";
    u.textContent = unit;
    el.append(u);
  }
}
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function api(path, opts = {}) {
  const r = await fetch(path, { headers: AUTH, ...opts });
  if (!r.ok) throw new Error(r.status + " " + path);
  return r.json();
}
async function postJSON(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { ...AUTH, "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error(r.status + " " + path);
  return r.json();
}

/* ---------- state ---------- */

let caps = null;
let capsSignature = "";       // rebuild the form only when availability changes
const lastJobState = new Map(); // job id -> state, to detect a change worth re-stamping
const lastNodeState = new Map();
let selectedJob = null;
let logStream = null;
let firstPaint = true;

/* ---------- poll loop ---------- */

async function refresh() {
  try {
    const [agg, nodes, jobs, usage, nextCaps] = await Promise.all([
      api("/api/aggregate"),
      api("/api/nodes"),
      api("/api/jobs"),
      api("/api/usage"),
      api("/api/capabilities"),
    ]);
    caps = nextCaps;
    renderBand(agg, nextCaps);
    renderNodes(nodes);
    renderJobs(jobs);
    renderUsage(usage);

    const sig = JSON.stringify([
      nextCaps.runtimes.map((r) => [r.value, r.node_count]),
      nextCaps.gpu_types.map((g) => [g.value, g.node_count, g.detail]),
      nextCaps.arches.map((a) => a.value),
      nextCaps.oses.map((o) => o.value),
      nextCaps.preemptible_nodes,
      nextCaps.max_ram_fast_mb,
      nextCaps.max_ram_swap_mb,
    ]);
    if (sig !== capsSignature) {
      capsSignature = sig;
      buildForm(nextCaps);
    }
    setConn(true);
    firstPaint = false;
  } catch (e) {
    setConn(false);
  }
}

function setConn(ok) {
  const el = $("conn");
  el.dataset.state = ok ? "live" : "down";
  $("conn-text").textContent = ok ? "live" : "control plane unreachable";
}

/* ---------- capacity band ---------- */

function renderBand(a, c) {
  // One job takes a whole machine in v1, so the cores that can accept work now
  // are the ones on idle machines. Reporting installed cores under a heading
  // that says "available" would overstate the pool.
  figure($("s-cpu"), String(a.vcpus_idle || 0), "cores");
  $("lab-cpu").textContent = `vCPUs free of ${a.vcpus} online`;

  // RAM is consumed, so the headline figure is what is free.
  const [freeN, freeU] = mbParts(a.ram_free_mb);
  figure($("s-ram"), freeN, freeU);
  $("lab-ram").textContent = `RAM free of ${mbText(a.ram_total_mb)}`;

  figure($("s-gpu"), String(a.gpu_count), a.gpu_count === 1 ? "device" : "devices");

  // VRAM needs care rather than a bare number. A CUDA card has dedicated VRAM
  // worth reporting. Apple Silicon shares one memory pool with the system, so
  // the agent reports no separate VRAM on purpose — printing "0 MB" would read
  // as "no GPU memory", and printing the RAM figure again would double-count
  // it. Say which it is instead.
  const metalCount = (c.gpu_types || []).filter((g) => g.value === "metal")
    .reduce((n, g) => n + g.node_count, 0);
  const cudaCount = (c.gpu_types || []).filter((g) => g.value === "cuda")
    .reduce((n, g) => n + g.node_count, 0);

  if (cudaCount === 0) {
    figure($("s-vram-cuda"), "—", "");
    $("lab-cuda").textContent = "CUDA VRAM · none online";
  } else {
    const cuda = mbParts(a.cuda_vram_mb);
    figure($("s-vram-cuda"), cuda[0], cuda[1]);
    $("lab-cuda").textContent = "CUDA VRAM";
  }

  if (metalCount === 0) {
    figure($("s-vram-metal"), "—", "");
    $("lab-metal").textContent = "Metal VRAM · none online";
  } else if (!a.metal_vram_mb) {
    figure($("s-vram-metal"), "unified", "");
    $("lab-metal").textContent = "Metal · shares the RAM above";
  } else {
    const metal = mbParts(a.metal_vram_mb);
    figure($("s-vram-metal"), metal[0], metal[1]);
    $("lab-metal").textContent = "Metal VRAM";
  }

  const sw = mbParts(a.swap_total_mb), dk = mbParts(a.disk_free_mb);
  figure($("s-swap"), sw[0], sw[1]);
  figure($("s-disk"), dk[0], dk[1]);
  $("lab-disk").textContent = "Disk free";

  const bits = [];
  if (a.online_nodes === 0) {
    bits.push("No machines online. Nothing can run until one joins.");
  } else {
    bits.push(`${a.online_nodes} online`);
    if (c && c.idle_nodes != null) bits.push(`${c.idle_nodes} idle`);
    bits.push(`${a.running_jobs} running`);
    bits.push(`${a.queued_jobs} queued`);
    if (a.offline_nodes) bits.push(`${a.offline_nodes} offline`);
    if (c && c.preemptible_nodes) bits.push(`${c.preemptible_nodes} preemptible`);
  }
  let foot = bits.join("  ·  ");
  foot += `  ·  ${mbText(a.disk_total_mb)} disk and ${a.vcpus} vCPU installed`;
  if (a.load_avg_1) foot += `, load ${a.load_avg_1.toFixed(2)}`;
  if (a.swap_total_mb) {
    foot += ".  Swap is disk standing in for RAM: allowed, far slower, " +
      "and never counted toward the RAM figure above.";
  }
  $("band-foot").textContent = foot;
}

/* ---------- node directory ---------- */

// Short wayfinding code for a machine, taken from its name so it is stable.
function nodeCode(name, i) {
  const letters = (name.match(/[a-z]/gi) || ["n"]).join("").toUpperCase();
  return letters.slice(0, 1) + (i + 1);
}

function nodeRow(n, i) {
  const el = document.createElement("article");
  el.className = "node";
  el.dataset.preempt = String(n.labels && n.labels.preemptible === "true");

  const gpuType = (n.labels && n.labels["gpu-type"]) || "none";
  const gpus = (n.gpus || [])
    .map((g) => g.model + (g.vram_mb ? " · " + mbText(g.vram_mb) : ""))
    .join(", ");
  const loadPct = n.cpu_count ? Math.min(100, (n.load_avg_1 / n.cpu_count) * 100) : 0;

  const tags = [];
  tags.push(`<span class="tag tag-${n.status === "online" ? "online" : "offline"}">${esc(n.status)}</span>`);
  if (gpuType !== "none") tags.push(`<span class="tag tag-gpu">${esc(gpuType)}</span>`);
  if (n.labels && n.labels.preemptible === "true")
    tags.push('<span class="tag tag-preempt">preemptible</span>');
  if (n.running_jobs > 0) tags.push('<span class="tag tag-busy">busy</span>');

  const runnable = Object.entries(n.toolchains || {})
    .filter(([k, v]) => v && k !== "docker")
    .map(([k]) => k);

  el.innerHTML = `
    <p class="node-code">${esc(nodeCode(n.name, i))}</p>
    <div class="node-head">
      <span class="node-name">${esc(n.name)}</span>
      ${n.labels && n.labels.location ? `<span class="node-where">${esc(n.labels.location)}</span>` : ""}
      ${tags.join("")}
    </div>
    <p class="node-spec">${n.cpu_count} vCPU · ${mbText(n.ram_free_mb)} free of ${mbText(n.ram_total_mb)} RAM · swap ${mbText(n.swap_total_mb)}
      <br>${esc(gpus || "no GPU")} · disk ${mbText(n.disk_free_mb)} free
      <br>${esc((n.labels && n.labels.arch) || "?")} · ${esc((n.labels && n.labels.os) || "?")}${n.docker_available ? " · docker" : ""}${runnable.length ? " · runs " + esc(runnable.join(", ")) : ""}</p>
    <div class="node-runs">load ${n.load_avg_1.toFixed(2)} · ${n.running_jobs} running
      <div class="load"><span style="width:${loadPct}%"></span></div>
    </div>`;

  // Signature move: a machine changing state re-stamps its row.
  const prev = lastNodeState.get(n.name);
  if (!firstPaint && prev && prev !== n.status) el.classList.add("restamp");
  lastNodeState.set(n.name, n.status);
  return el;
}

function renderNodes(nodes) {
  const on = $("nodes"), off = $("nodes-offline");
  on.textContent = ""; off.textContent = "";
  let offCount = 0, i = 0;
  nodes.forEach((n) => {
    if (n.status === "online") on.appendChild(nodeRow(n, i++));
    else { off.appendChild(nodeRow(n, offCount++)); }
  });
  if (!on.children.length) {
    on.innerHTML = '<p class="empty">No machines online. Use “Add a machine” below to join one.</p>';
  }
  $("offline-block").hidden = offCount === 0;
}

/* ---------- jobs ---------- */

function renderJobs(jobs) {
  const body = $("jobs-body");
  body.textContent = "";
  $("jobs-empty").hidden = jobs.length > 0;

  jobs.forEach((j) => {
    const tr = document.createElement("tr");
    tr.tabIndex = 0;
    tr.dataset.selected = String(selectedJob === j.id);
    tr.dataset.state = j.state;

    const lane = j.lane === "slow"
      ? '<span class="lane-slow">slow · swap</span>'
      : (j.lane ? '<span class="lane-fast">fast</span>' : "—");
    const exit = j.exit_code === null || j.exit_code === undefined ? "—" : j.exit_code;
    // Sequential code tied to submit order; the hex id stays the URL key.
    const code = j.seq ? "J" + j.seq : j.id.slice(0, 6);

    tr.innerHTML = `
      <td class="code code-cell" data-label="Code">${esc(code)}</td>
      <td class="lab-cell" data-label="Label" title="${esc(j.name)}">${esc(j.name)}</td>
      <td data-label="Runtime">${esc(j.runtime)}</td>
      <td class="state-cell" data-label="State"><span class="state state-${esc(j.state)}">${esc(j.state)}</span></td>
      <td class="code" data-label="Machine">${esc(j.assigned_node || "not yet placed")}</td>
      <td data-label="Lane">${lane}</td>
      <td class="num code" data-label="Exit">${esc(exit)}</td>
      <td class="num code" data-label="Files">${j.artifact_count || 0}</td>
      <td class="act"><button class="linkbtn" type="button">Read log</button></td>`;

    const open = () => openLog(j.id, j.name);
    tr.querySelector(".linkbtn").addEventListener("click", (e) => { e.stopPropagation(); open(); });
    tr.addEventListener("click", open);
    tr.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(); }
    });

    const prev = lastJobState.get(j.id);
    if (!firstPaint && prev && prev !== j.state) tr.classList.add("restamp");
    lastJobState.set(j.id, j.state);

    body.appendChild(tr);
  });
}

/* ---------- log panel ---------- */

function openLog(id, name) {
  selectedJob = id;
  $("log-title").textContent = "Log: " + name;
  $("log-body").textContent = "";
  $("artifact-list").textContent = "";
  $("logpanel").hidden = false;
  document.querySelectorAll("#jobs-body tr").forEach((tr) => (tr.dataset.selected = "false"));

  if (logStream) logStream.close();
  logStream = new EventSource(`/api/jobs/${id}/logs/stream${QTOK}`);
  logStream.onmessage = (ev) => {
    const l = JSON.parse(ev.data);
    const line = document.createElement("div");
    line.className = l.stream;
    line.textContent = l.text;
    const box = $("log-body");
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    box.appendChild(line);
    if (atBottom) box.scrollTop = box.scrollHeight;
  };
  logStream.addEventListener("done", () => { logStream.close(); loadArtifacts(id); });
  logStream.onerror = () => { loadArtifacts(id); };
  loadArtifacts(id);
  $("logpanel").scrollIntoView({ behavior: "smooth", block: "nearest" });
}

async function loadArtifacts(id) {
  try {
    const files = await api(`/api/jobs/${id}/artifacts`);
    const box = $("artifact-list");
    box.textContent = "";
    if (!files.length) return;
    files.forEach((f) => {
      const a = document.createElement("a");
      a.href = `/api/jobs/${id}/artifacts/${encodeURIComponent(f)}${QTOK}`;
      a.textContent = f;
      a.download = f;
      box.appendChild(a);
    });
  } catch (_) { /* job may have no artifacts yet */ }
}

$("log-close").addEventListener("click", () => {
  $("logpanel").hidden = true;
  selectedJob = null;
  if (logStream) logStream.close();
  document.querySelectorAll("#jobs-body tr").forEach((tr) => (tr.dataset.selected = "false"));
});

/* ---------- usage ---------- */

function renderUsage(u) {
  const kv = (m) => {
    const e = Object.entries(m || {});
    return e.length ? e.map(([k, v]) => `${k} ${v}`).join("  ·  ") : "none yet";
  };
  $("usage").innerHTML = `
    <div class="usage-line"><span class="usage-key">Total</span><span>${u.total_jobs} jobs submitted</span></div>
    <div class="usage-line"><span class="usage-key">Ran in swap</span><span>${u.slow_lane_jobs} slow-lane runs</span></div>
    <div class="usage-line"><span class="usage-key">By state</span><span>${esc(kv(u.by_state))}</span></div>
    <div class="usage-line"><span class="usage-key">By runtime</span><span>${esc(kv(u.by_runtime))}</span></div>
    <div class="usage-line"><span class="usage-key">By machine</span><span>${esc(kv(u.by_node))}</span></div>`;
}

/* ---------- availability-aware form ---------- */

// The full set of values the scheduler understands. Anything the pool cannot
// currently satisfy is rendered disabled with the reason, rather than hidden,
// so the operator can see that the option exists and why it is unavailable.
const ALL_GPU = [
  { value: "", label: "No GPU required" },
  { value: "cuda", label: "CUDA (NVIDIA)" },
  { value: "metal", label: "Metal (Apple)" },
];
const ALL_ARCH = [
  { value: "", label: "Any architecture" },
  { value: "amd64", label: "amd64 (x86-64)" },
  { value: "arm64", label: "arm64" },
];
const ALL_OS = [
  { value: "", label: "Any OS" },
  { value: "linux", label: "Linux" },
  { value: "darwin", label: "macOS" },
  { value: "windows", label: "Windows" },
];

function fillSelect(sel, all, available) {
  const byValue = new Map(available.map((o) => [o.value, o]));
  const keep = sel.value;
  sel.textContent = "";
  all.forEach((o) => {
    const opt = document.createElement("option");
    opt.value = o.value;
    if (o.value === "") {
      opt.textContent = o.label;
    } else {
      const live = byValue.get(o.value);
      if (live) {
        opt.textContent = `${o.label} · ${live.detail}`;
      } else {
        opt.textContent = `${o.label} · none online`;
        opt.disabled = true;
      }
    }
    sel.appendChild(opt);
  });
  // Preserve the previous choice when it is still offered.
  const target = Array.from(sel.options).find((o) => o.value === keep && !o.disabled);
  sel.value = target ? keep : "";
}

function buildForm(c) {
  fillSelect($("f-gpu"), ALL_GPU, c.gpu_types);
  fillSelect($("f-arch"), ALL_ARCH, c.arches);
  fillSelect($("f-os"), ALL_OS, c.oses);

  const gpuNames = c.gpu_types.map((g) => g.detail).filter(Boolean);
  $("gpu-hint").textContent = gpuNames.length
    ? "Online: " + gpuNames.join(" · ")
    : "No online machine reports a GPU.";

  $("ram-hint").textContent = c.online_nodes
    ? `Largest machine offers ${mbText(c.max_ram_fast_mb)} free RAM, or ` +
      `${mbText(c.max_ram_swap_mb)} counting swap (slow lane). Leave 0 to skip the check.`
    : "No machines online.";
  $("f-ram").max = String(c.max_ram_swap_mb || 0);
  $("f-timeout").placeholder = String(c.default_timeout_sec || 3600);

  const hasPreempt = c.preemptible_nodes > 0;
  const box = $("f-preempt");
  box.disabled = !hasPreempt;
  if (!hasPreempt) box.checked = false;
  $("preempt-wrap").dataset.disabled = String(!hasPreempt);
  $("preempt-note").textContent = hasPreempt
    ? `${c.preemptible_nodes} machine(s) can vanish mid-run, such as Colab sessions. Only short or checkpointed work.`
    : "No preemptible machines are online, so there is nothing to opt in to.";

  // Runtime availability, driven by what agents report they can execute.
  const unavailable = c.runtimes.filter((r) => !r.available).map((r) => r.label);
  const availableList = c.runtimes.filter((r) => r.available).map((r) => r.label);
  const note = $("runtime-note");
  note.textContent =
    "A .py, .ipynb or .java file. Add requirements.txt and it is installed first.";
  if (availableList.length) {
    note.textContent += `  Runnable now: ${availableList.join(", ")}.`;
  }
  if (unavailable.length) {
    note.textContent += `  No machine can run: ${unavailable.join(", ")}.`;
  }
  checkFeasibility();
}

function currentRequirements() {
  return {
    runtime: inferRuntime(),
    needs_gpu: $("f-gpu").value,
    min_ram_mb: Number($("f-ram").value || 0),
    needs_arch: $("f-arch").value,
    needs_os: $("f-os").value,
    allow_preempt: $("f-preempt").checked,
    disk_backed: $("f-disk").checked,
  };
}

function inferRuntime() {
  const f = $("f-files").files;
  for (const file of f) {
    const n = file.name.toLowerCase();
    if (n.endsWith(".py")) return "python";
    if (n.endsWith(".ipynb")) return "notebook";
    if (n.endsWith(".java")) return "java";
  }
  return "";
}

let feasTimer = null;
let lastVerdict = null;
function checkFeasibility() {
  clearTimeout(feasTimer);
  feasTimer = setTimeout(async () => {
    const out = $("verdict");
    try {
      const v = await postJSON("/api/feasibility", currentRequirements());
      lastVerdict = v;
      out.textContent = v.reason;
      out.dataset.state = !v.placeable ? "bad" : v.slow_lane_only ? "slow" : "ok";
      $("btn-submit").disabled = !v.placeable;
      $("btn-queue").hidden = v.placeable;
    } catch (e) {
      out.dataset.state = "idle";
      $("btn-submit").disabled = false;
      $("btn-queue").hidden = true;
    }
  }, 180);
}

["f-gpu", "f-ram", "f-arch", "f-os", "f-preempt", "f-disk", "f-files"].forEach((id) => {
  const el = $(id);
  el.addEventListener("change", checkFeasibility);
  if (el.type === "number") el.addEventListener("input", checkFeasibility);
});

// Report the chosen files in the product's own words, since the native
// control's own text is hidden.
$("f-files").addEventListener("change", () => {
  const files = Array.from($("f-files").files);
  const out = $("file-name");
  if (!files.length) {
    out.textContent = "nothing chosen yet";
    out.dataset.has = "false";
    return;
  }
  out.textContent = files.length === 1
    ? files[0].name
    : `${files[0].name} and ${files.length - 1} more`;
  out.dataset.has = "true";
});

/* ---------- submit ---------- */

async function doSubmit() {
  const msg = $("submit-msg");
  const files = $("f-files").files;
  if (!files.length) {
    msg.textContent = "Choose a file first.";
    msg.dataset.state = "err";
    return;
  }
  msg.textContent = "submitting…";
  msg.dataset.state = "";

  const fd = new FormData();
  for (const f of files) fd.append("files", f);
  const add = (k, v) => { if (v !== "" && v !== 0 && v !== false) fd.append(k, v); };
  add("needs_gpu", $("f-gpu").value);
  add("min_ram_mb", $("f-ram").value);
  add("timeout_sec", $("f-timeout").value);
  add("needs_arch", $("f-arch").value);
  add("needs_os", $("f-os").value);
  add("name", $("f-name").value);
  if ($("f-preempt").checked) fd.append("allow_preempt", "true");
  if ($("f-disk").checked) fd.append("disk_backed", "true");

  try {
    const r = await fetch("/api/jobs", { method: "POST", headers: AUTH, body: fd });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || r.statusText);
    msg.textContent = `Queued ${j.seq ? "J" + j.seq : j.id.slice(0, 6)}: ${j.name}`;
    msg.dataset.state = "ok";
    $("submit-form").reset();
    $("file-name").textContent = "nothing chosen yet";
    $("file-name").dataset.has = "false";
    checkFeasibility();
    refresh();
  } catch (err) {
    msg.textContent = "Could not submit: " + err.message;
    msg.dataset.state = "err";
  }
}

$("submit-form").addEventListener("submit", (e) => { e.preventDefault(); doSubmit(); });
$("btn-queue").addEventListener("click", doSubmit);

/* ---------- Colab ---------- */

async function loadColab() {
  try {
    const c = await api("/api/colab");
    $("colab-snippet").textContent = c.snippet;
    $("colab-caveat").textContent = c.caveat;
    $("colab-open").href = c.notebook_url;

    $("tiers-body").innerHTML = c.tiers.map((t) => `
      <tr>
        <td class="lab-cell" data-label="Runtime">${esc(t.runtime)}</td>
        <td data-label="Accelerator">${esc(t.gpu || "none")}</td>
        <td class="num" data-label="VRAM">${t.vram_gb ? t.vram_gb + " GB" : "—"}</td>
        <td class="num" data-label="RAM">${t.ram_gb} GB</td>
        <td class="num" data-label="High-RAM">${t.high_ram_gb ? t.high_ram_gb + " GB" : "not offered"}</td>
        <td class="num" data-label="vCPU">${t.vcpus}</td>
        <td data-label="Pool label"><span class="pool-label">gpu-type=${esc(t.gpu_type)}</span></td>
      </tr>`).join("");

    const m = $("colab-measured");
    if (!c.measured || !c.measured.length) {
      m.innerHTML = '<p class="empty" style="padding:0">No Colab machine has joined yet. ' +
        'When one does, its real figures appear here. Trust those over the table above.</p>';
    } else {
      m.innerHTML = c.measured.map((x) => `
        <div class="measured-row">
          <span>${esc(x.node_name)}</span>
          <span>${x.online ? "online" : "offline"}</span>
          <span>${esc(x.gpu || "no GPU")}</span>
          <span>VRAM ${mbText(x.vram_mb)}</span>
          <span>RAM ${mbText(x.ram_mb)}</span>
          <span>${x.vcpus} vCPU</span>
        </div>`).join("");
    }
  } catch (e) {
    $("colab-snippet").textContent = "Could not load the Colab cell: " + e.message;
  }
}

async function copyText(text, btn) {
  const original = btn.lastChild.textContent;
  try {
    await navigator.clipboard.writeText(text);
    btn.lastChild.textContent = " Copied";
  } catch (_) {
    // Clipboard API needs a secure context; fall back to a selection.
    const ta = document.createElement("textarea");
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); btn.lastChild.textContent = " Copied"; }
    catch (__) { btn.lastChild.textContent = " Press ⌘C"; }
    ta.remove();
  }
  setTimeout(() => { btn.lastChild.textContent = original; }, 1800);
}

$("colab-copy").addEventListener("click", (e) =>
  copyText($("colab-snippet").textContent, e.currentTarget));

/* ---------- add a machine ---------- */

async function loadEnroll() {
  try {
    const e = await api("/api/enroll");
    const tabs = $("enroll-tabs"), panels = $("enroll-panels");
    tabs.textContent = ""; panels.textContent = "";

    e.recipes.forEach((r, i) => {
      const tab = document.createElement("button");
      tab.className = "tab";
      tab.type = "button";
      tab.setAttribute("role", "tab");
      tab.id = "tab-" + r.id;
      tab.setAttribute("aria-controls", "panel-" + r.id);
      tab.setAttribute("aria-selected", String(i === 0));
      tab.textContent = r.label;
      tabs.appendChild(tab);

      const panel = document.createElement("div");
      panel.className = "panel";
      panel.id = "panel-" + r.id;
      panel.setAttribute("role", "tabpanel");
      panel.setAttribute("aria-labelledby", tab.id);
      panel.hidden = i !== 0;
      panel.innerHTML = `
        <p>${esc(r.note)}</p>
        <pre class="cmd">${esc(r.command)}</pre>
        <div class="copyrow">
          <button class="btn btn-ghost" type="button">
            <svg class="btn-ico" viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
            <span>Copy</span>
          </button>
        </div>`;
      panel.querySelector("button").addEventListener("click", (ev) =>
        copyText(r.command, ev.currentTarget));
      panels.appendChild(panel);

      tab.addEventListener("click", () => {
        tabs.querySelectorAll(".tab").forEach((t) => t.setAttribute("aria-selected", "false"));
        tab.setAttribute("aria-selected", "true");
        panels.querySelectorAll(".panel").forEach((p) => (p.hidden = true));
        panel.hidden = false;
      });
    });
  } catch (err) {
    $("enroll-panels").innerHTML =
      `<p class="empty">Could not load onboarding commands: ${esc(err.message)}</p>`;
  }
}

/* ---------- boot ---------- */

refresh();
loadColab();
loadEnroll();
setInterval(refresh, 3000);
