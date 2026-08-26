// The dependency graph behind `gg status -visual`.
//
// Cytoscape draws and dagre ranks; this file decides what a service looks like
// and when the picture is allowed to move. The second half of that is the part
// worth being careful about: the page re-fetches every three seconds, and a
// graph that re-lays-out on a timer is unusable however good the layout is. So
// the topology is fingerprinted, and only a change in the fingerprint earns a
// new layout. Everything else — ready counts, colours — is updated in place.

cytoscape.use(cytoscapeDagre);

const css = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();

function stateOf(s) {
  const a = s.actual || {};
  if (!s.in_sync || !a.exists) return "out-of-sync";
  if ((a.ready_replicas || 0) < (a.desired_replicas || 0)) return "starting";
  return "running";
}

const colourOf = {
  running: () => css("--ok"),
  starting: () => css("--warn"),
  "out-of-sync": () => css("--bad"),
};

// gagarin only runs a project's own images, so every reference starts with the
// same registry and project id. Showing it on every node would crowd out the
// part that differs.
function shortImage(image, projectID) {
  const i = image.indexOf("/" + projectID + "/");
  return i >= 0 ? image.slice(i + projectID.length + 2) : image;
}

let cy = null;
let lastShape = "";

function style() {
  return [
    {
      selector: "node",
      style: {
        shape: "round-rectangle",
        width: 162, height: 58,
        "background-color": css("--panel"),
        // A broken border is the default because private is the default. Colour
        // says how the service is doing; the border style says who can reach it.
        // Keeping those on two independent channels is what lets one glance
        // answer both.
        //
        // An explicit pattern rather than the `dotted` preset, whose dots are
        // short enough at this weight to read as stippling instead of as a line
        // deliberately broken up.
        "border-width": 2,
        "border-style": "dashed",
        "border-dash-pattern": [7, 5],
        "border-color": (n) => colourOf[n.data("state")](),
        label: (n) => n.data("label"),
        "text-wrap": "wrap",
        "text-valign": "center", "text-halign": "center",
        color: css("--text"),
        "font-size": 13, "font-weight": 500,
        "line-height": 1.5,
        "font-family": 'ui-sans-serif,-apple-system,"Segoe UI",Roboto,Inter,sans-serif',
      },
    },
    {
      // Solid means the internet can reach it. An unbroken line for something
      // exposed and a broken one for something enclosed is the way round that
      // needs no explaining.
      selector: "node.public",
      style: { "border-style": "solid" },
    },
    {
      // Border style is spoken for, so a failing service is called out by weight
      // instead. Colour alone would put this beyond a fair number of readers,
      // and it is the one state nobody can afford to miss.
      selector: 'node[state = "out-of-sync"]',
      style: { "border-width": 3 },
    },
    { selector: "node:selected", style: { "overlay-opacity": 0 } },
    {
      selector: "edge",
      style: {
        width: 1.8,
        "line-color": css("--line"),
        "target-arrow-color": css("--line"),
        "target-arrow-shape": "triangle",
        "arrow-scale": 1.1,
        "curve-style": "bezier",
      },
    },
    {
      // Hovering a service lights up what it reaches, which is the question the
      // page exists to answer.
      selector: "edge.lit",
      style: { "line-color": css("--link"), "target-arrow-color": css("--link"), width: 2.6, "z-index": 9 },
    },
    { selector: "node.dim", style: { opacity: 0.35 } },
    { selector: "edge.dim", style: { opacity: 0.15 } },
  ];
}

// Fit, but only so far. A three-service project fitted to a wide window would
// otherwise blow the boxes up to twice their size, which reads as a rendering
// fault rather than as a deliberate close-up. A little enlargement keeps a small
// graph from looking lost; past that, zooming in is the user's to ask for.
const MAX_FIT = 1.25;

function fit() {
  cy.fit(undefined, 46);
  if (cy.zoom() > MAX_FIT) {
    cy.zoom(MAX_FIT);
    cy.center();
  }
}

const LAYOUT = {
  name: "dagre",
  rankDir: "TB",     // callers on top, what they rest on underneath
  nodeSep: 40, rankSep: 78, edgeSep: 18,
  animate: false,
  padding: 46,
};

function build(st) {
  const svcs = (st.services || []).slice().sort((a, b) => a.name.localeCompare(b.name));
  const have = new Set(svcs.map((s) => s.name));

  const nodes = svcs.map((s) => {
    const a = s.actual || {};
    return {
      data: {
        id: s.name,
        state: stateOf(s),
        label: s.name + "\n" + (a.ready_replicas || 0) + "/" + (a.desired_replicas || 0)
          + "  ·  :" + s.port,
        svc: s,
        url: s.url || "",
      },
      classes: s.public ? "public" : "",
    };
  });

  const edges = [];
  for (const s of svcs) {
    // Filtered against the services that exist, so an edge whose target was just
    // destroyed does not produce a phantom node.
    for (const d of (s.needs || []).filter((n) => have.has(n))) {
      edges.push({ data: { id: s.name + "->" + d, source: s.name, target: d } });
    }
  }
  return { nodes, edges };
}

// What has to change before the picture is allowed to move.
function shapeOf(el) {
  return el.nodes.map((n) => n.data.id).join(",") + "|" +
    el.edges.map((e) => e.data.id).sort().join(",");
}

// The card is built as HTML, and everything in it — service names, image
// references, whatever the container runtime chose to say — is server data
// rather than ours. Escaped rather than trusted.
function esc(v) {
  return String(v).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function tipHTML(s, projectID) {
  const a = s.actual || {};
  const rows = [];
  rows.push(["image", shortImage(s.image, projectID)]);
  rows.push(["port", String(s.port)]);
  rows.push(["reaches", (s.needs || []).length ? s.needs.join(", ") : "nothing else"]);
  if (s.volume_path) rows.push(["volume", s.volume_size_gb + "GB at " + s.volume_path]);
  rows.push([s.public ? "url" : "access", s.public ? s.url : "private — in-project only"]);
  if (stateOf(s) === "out-of-sync" && a.message) rows.push(["cluster", a.message, "clamp"]);
  return "<b>" + esc(s.name) + "</b>" +
    rows.map((r) => '<div class="row ' + (r[2] || "") + '">' + r[0] +
      " <span>" + esc(r[1]) + "</span></div>").join("");
}

function render(st) {
  const el = build(st);

  document.getElementById("empty").style.display = el.nodes.length ? "none" : "flex";
  if (!el.nodes.length) {
    document.getElementById("empty").innerHTML =
      "No services yet — ship one with <code class='mono'>&nbsp;gg ship &lt;project&gt;/web:8080</code>";
    if (cy) cy.elements().remove();
    return;
  }

  if (!cy) {
    cy = cytoscape({
      container: document.getElementById("cy"),
      elements: el,
      style: style(),
      minZoom: 0.25, maxZoom: 2.5,
      wheelSensitivity: 0.2,
    });
    wire();
    cy.layout(LAYOUT).run();
    fit();
    lastShape = shapeOf(el);
  } else {
    const shape = shapeOf(el);
    if (shape !== lastShape) {
      // The graph itself changed — a deploy added a service or moved an edge.
      // Worth a new layout, and worth the user losing their pan for it.
      cy.elements().remove();
      cy.add(el);
      cy.layout(LAYOUT).run();
      fit();
      lastShape = shape;
    } else {
      // Same shape, new numbers. Patch in place so the view does not move under
      // somebody who is reading it.
      cy.batch(() => {
        for (const n of el.nodes) {
          const node = cy.$id(n.data.id);
          node.data("state", n.data.state);
          node.data("label", n.data.label);
          node.data("svc", n.data.svc);
          node.data("url", n.data.url);
        }
      });
    }
  }

  cy.scratch("_project", st.project_id);
  legend(st);
}

function wire() {
  const tip = document.getElementById("tip");

  cy.on("mouseover", "node", (e) => {
    const n = e.target;
    const near = n.closedNeighborhood();
    cy.elements().addClass("dim");
    near.removeClass("dim");
    n.outgoers("edge").removeClass("dim").addClass("lit");
    tip.innerHTML = tipHTML(n.data("svc"), cy.scratch("_project"));
    tip.style.display = "block";
    document.getElementById("cy").style.cursor = n.data("url") ? "pointer" : "default";
  });

  cy.on("mousemove", (e) => {
    if (tip.style.display !== "block") return;
    const p = e.renderedPosition || { x: 0, y: 0 };
    // Kept inside the window, so a node near the right edge does not push the
    // card off screen.
    const w = tip.offsetWidth, h = tip.offsetHeight;
    let x = p.x + 18, y = p.y + 53 + 18;
    if (x + w > window.innerWidth - 12) x = p.x - w - 18;
    if (y + h > window.innerHeight - 12) y = y - h - 36;
    tip.style.left = x + "px";
    tip.style.top = y + "px";
  });

  cy.on("mouseout", "node", () => {
    cy.elements().removeClass("dim").removeClass("lit");
    tip.style.display = "none";
    document.getElementById("cy").style.cursor = "default";
  });

  // A public service is the one thing on this page worth clicking.
  cy.on("tap", "node", (e) => {
    const url = e.target.data("url");
    if (url) window.open(url, "_blank", "noopener");
  });

  document.getElementById("fit").onclick = fit;
}

// Two keys, because there are two channels and conflating them in one row is
// what makes a legend look like a list of unrelated facts. Only states actually
// on screen are named — a key explaining a colour nobody can see is how readers
// learn to skip keys.
function legend(st) {
  const svcs = st.services || [];
  const seen = new Set(svcs.map(stateOf));
  const chip = (v, label) => '<span><i style="background:' + v + '"></i>' + label + "</span>";

  const health = [];
  if (seen.has("running")) health.push(chip(css("--ok"), "running"));
  if (seen.has("starting")) health.push(chip(css("--warn"), "starting"));
  if (seen.has("out-of-sync")) health.push(chip(css("--bad"), "failing"));

  const reach = [];
  if (svcs.some((s) => s.public)) reach.push('<span><b class="k solid"></b>public</span>');
  if (svcs.some((s) => !s.public)) reach.push('<span><b class="k dotted"></b>private</span>');
  reach.push("<span>→ reaches</span>");

  const grp = (inner) => '<div class="grp">' + inner + '</div>';
  document.getElementById("legend").innerHTML =
    grp(health.join("")) + grp(reach.join("")) +
    grp('<span>hover a service · click a public one</span>');
}

function setLive(cls, txt) {
  document.getElementById("dot").className = "dot " + cls;
  document.getElementById("livetxt").textContent = txt;
}

async function tick() {
  try {
    const r = await fetch("/data", { cache: "no-store" });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || "HTTP " + r.status);
    document.getElementById("err").style.display = "none";
    document.getElementById("proj").textContent = j.project;
    document.getElementById("pid").textContent = j.project_id;
    document.title = "gagarin — " + j.project;
    render(j);
    setLive("", "live");
  } catch (e) {
    // The last good picture stays up. A blank page is a worse answer than a
    // stale one, as long as it admits which it is.
    const box = document.getElementById("err");
    box.style.display = "block";
    box.textContent = String(e.message || e);
    setLive("err", "not updating");
  }
}

tick();
setInterval(tick, 3000);

// Following the system theme means re-reading the palette, since Cytoscape
// resolved the old one into canvas paint at style time.
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  if (cy) cy.style(style()).update();
});
