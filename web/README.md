# Vendored assets for `gg status -visual`

Checked in rather than fetched at runtime, and served from the binary. The
command is meant to work on a laptop that can reach the gagarin API and nothing
else; a page that pulls a layout library off a CDN fails exactly when someone is
debugging a network problem, which is when they are most likely to be looking at
it. It also means no third party learns the names of anyone's services.

Roughly 670 KB of JavaScript. Refresh by re-downloading at the pinned version and
running `task test` in the engine harness against a project with a few services.

| File | Version | Source | Licence |
|---|---|---|---|
| `cytoscape.min.js` | 3.30.2 | https://unpkg.com/cytoscape@3.30.2/dist/cytoscape.min.js | MIT |
| `dagre.min.js` | 0.8.5 | https://unpkg.com/dagre@0.8.5/dist/dagre.min.js | MIT |
| `cytoscape-dagre.js` | 2.5.0 | https://unpkg.com/cytoscape-dagre@2.5.0/cytoscape-dagre.js | MIT |

`index.html` and `app.js` are ours.

Cytoscape does the rendering and interaction; dagre does the hierarchical layout,
which is the part worth not writing by hand — edge routing around intermediate
ranks is where a hand-rolled layered layout stops looking deliberate.
