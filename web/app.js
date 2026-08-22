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
        "border-width": 1.5,
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
      // Dashed border for a service that is not doing what it was told. Colour
      // alone would leave the distinction invisible to a good number of readers.
      selector: 'node[state = "out-of-sync"]',
      style: { "border-style": "dashed" },
    },
    {
      selector: "node.public",
      style: { "background-color": css("--panel2") },
    },
    {
      selector: "node:selected",
      style: { "border-width": 3, "overlay-opacity": 0 },
    },
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

function tipHTML(s, projectID) {
  const a = s.actual || {};
  const rows = [];
  rows.push(["image", shortImage(s.image, projectID)]);
  rows.push(["port", String(s.port)]);
  rows.push(["reaches", (s.needs || []).length ? s.needs.join(", ") : "nothing else"]);
  if (s.volume_path) rows.push(["volume", s.volume_size_gb + "GB at " + s.volume_path]);
  rows.push([s.public ? "url" : "access", s.public ? s.url : "private — in-project only"]);
  if (stateOf(s) === "out-of-sync" && a.message) rows.push(["cluster", a.message]);
  return "<b>" + s.name + "</b>" +
    rows.map((r) => '<div class="row">' + r[0] + " <span>" + r[1] + "</span></div>").join("");
}

function render(st) {
  const el = build(st);

  document.getElementById("empty").style.display = el.nodes.length ? "none" : "flex";
  if (!el.nodes.length) {
    document.getElementById("empty").innerHTML =
      "No services yet — deploy one with <code class='mono'>&nbsp;gg deploy</code>";
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

function legend(st) {
  const seen = new Set((st.services || []).map(stateOf));
  const bits = [];
  const chip = (v, label) => '<span><i style="background:' + v + '"></i>' + label + "</span>";
  if (seen.has("running")) bits.push(chip(css("--ok"), "running"));
  if (seen.has("starting")) bits.push(chip(css("--warn"), "starting"));
  if (seen.has("out-of-sync")) bits.push(chip(css("--bad"), "not what was asked for"));
  bits.push("<span>→ reaches</span>");
  bits.push("<span>hover a service · click a public one</span>");
  document.getElementById("legend").innerHTML = bits.join("");
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
