// Pipeline editor. The server validates and saves each change. Nodes use the
// viewer's markup. Text goes in as textContent; every URL is relative.
(function () {
	"use strict";

	const $ = (id) => document.getElementById(id);
	const meta = document.querySelector('meta[name="splice-action"]');
	const action = meta ? meta.content : "";

	function el(tag, cls, text) {
		const e = document.createElement(tag);
		if (cls) e.className = cls;
		if (text !== undefined) e.textContent = text;
		return e;
	}

	function api(method, url, body) {
		const headers = { "X-Splice-Action": action };
		if (body !== undefined) headers["Content-Type"] = "application/json";
		return fetch(url, { method: method, headers: headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: "same-origin" })
			.catch(() => { throw new Error("Cannot reach Splice. It stopped, or the network failed."); })
			.then((r) => r.text().then((t) => {
				let d = {};
				try { d = t ? JSON.parse(t) : {}; } catch (e) { d = {}; }
				if (r.ok) return d;
				const err = new Error(r.status === 403
					? "This page lost its session: the terminal session changed, or Splice stopped. Run /pipeline edit in Splice for a new link."
					: d.error || "Splice refused the request (" + r.status + ").");
				err.status = r.status;
				throw err;
			}));
	}

	if ($("home")) home();
	if ($("editor")) editor();

	function home() {
		const errEl = $("home-error");
		const fail = (err) => { errEl.hidden = false; errEl.textContent = err.message; };
		document.addEventListener("click", (e) => {
			const b = e.target.closest("button");
			if (!b) return;
			if (b.dataset.from) {
				b.disabled = true;
				api("POST", "drafts", { from: b.dataset.from, name: b.dataset.name || "", new: b.dataset.from !== "library" })
					.then((d) => { location.href = "?draft=" + encodeURIComponent(d.id); })
					.catch((err) => { b.disabled = false; fail(err); });
			} else if (b.dataset.delete) {
				if (!window.confirm("Delete this draft? The library does not change.")) return;
				api("DELETE", "draft?id=" + encodeURIComponent(b.dataset.delete))
					.then(() => { const li = b.closest("li"); const next = li.nextElementSibling || li.previousElementSibling; li.remove(); if (next) next.querySelector("a").focus(); })
					.catch(fail);
			}
		});
	}

	function editor() {
		const boot = JSON.parse(document.body.dataset.boot);
		const NW = 196, NH = 84, PAD = 12, GAP = 44;
		const TIERS = ["trivial", "light", "standard", "substantial", "architectural"];
		const types = {};
		boot.types.forEach((t) => { types[t.name] = t; });
		const rules = {};
		boot.permissions.forEach((p) => { rules[p.value] = p.rule; });

		let doc = boot.doc, pos = boot.positions || [], check = boot.check;
		let sel = null; // {k: "node" | "edge", i}
		let link = null; // keyboard connect mode: {from, target}
		let after = -1; // the node that "+" adds after, or -1
		let undo = [], redo = [], lastKey = "", lastAt = 0;
		let saving = false, dirty = false, saveTimer = 0, pending = Promise.resolve(), lost = "", savedAt = 0;
		let sayTimer = 0, consent = null, drag = null, raf = 0, rendering = false, zoom = 1, tab = "form";

		const wrap = $("wrap"), svg = $("plane"), nodesG = $("node-g"), edgesG = $("edge-g"), rubber = $("rubber");
		const panel = $("panel"), topoForm = $("topo-form"), nodeForm = $("node-form"), dialog = $("pub");
		const ns = svg.namespaceURI;
		const svgEl = (tag, attrs) => {
			const e = document.createElementNS(ns, tag);
			Object.keys(attrs || {}).forEach((k) => e.setAttribute(k, attrs[k]));
			return e;
		};

		function norm() {
			if (!Array.isArray(doc.nodes)) doc.nodes = [];
			if (!Array.isArray(doc.edges)) doc.edges = [];
			for (let i = pos.length; i < doc.nodes.length; i++) pos.push({ x: PAD, y: PAD + i * (NH + GAP) });
			pos.length = doc.nodes.length;
		}
		norm();

		function facts(type) {
			const t = types[type] || {};
			const mf = !!t.model_free, builtin = !!t.builtin;
			return {
				mf: mf, builtin: builtin, model: !mf, budget: !mf, prompt: type === "prompt" || type === "agent",
				command: type === "command", agent: type === "agent",
				pullctx: builtin && !mf, pullmem: !mf, verif: builtin, caps: !mf || builtin
			};
		}
		function indexOfName(name) {
			for (let i = 0; i < doc.nodes.length; i++) if (doc.nodes[i].name === name) return i;
			return -1;
		}
		const unique = (name) => doc.nodes.filter((n) => n.name === name).length === 1;
		const plural = (n, word) => n + " " + word + (n === 1 ? "" : "s");

		// History and autosave.
		const snap = () => JSON.stringify({ doc: doc, pos: pos });
		// commit applies one change. One key in a short burst is one undo step.
		// A change that changes nothing makes no undo step and no save.
		function commit(fn, key) {
			const now = Date.now(), before = snap();
			fn();
			if (snap() === before) return false;
			if (!(key && key === lastKey && now - lastAt < 1200)) {
				undo.push(before);
				if (undo.length > 200) undo.shift();
			}
			lastKey = key || "";
			lastAt = now;
			redo.length = 0;
			render();
			scheduleSave();
			return true;
		}
		function travel(from, to, label) {
			if (!from.length) return say("Nothing to " + label + ".");
			to.push(snap());
			const v = JSON.parse(from.pop());
			doc = v.doc;
			pos = v.pos;
			lastKey = "";
			if (sel && sel.i >= (sel.k === "node" ? doc.nodes : doc.edges).length) sel = null;
			render();
			fillPanel();
			scheduleSave();
			say(label === "undo" ? "Undone." : "Redone.");
		}
		function scheduleSave() {
			dirty = true;
			clearTimeout(saveTimer);
			saveTimer = setTimeout(flush, 350);
			renderHead();
			argvLine();
		}
		function flush() {
			clearTimeout(saveTimer);
			if (saving || !dirty) return pending;
			saving = true;
			dirty = false;
			renderHead();
			pending = api("PUT", "draft?id=" + encodeURIComponent(boot.id), { doc: doc, positions: pos })
				.then((d) => { check = d; savedAt = Date.now(); lost = ""; })
				.catch((err) => { lost = err.message; dirty = true; })
				.then(() => { saving = false; if (dirty && !lost) return flush(); render(); showErrors(); });
			return pending;
		}
		// settle waits until the draft is saved. After a failed save it tries
		// once more, so Validate and Publish retry without a new edit.
		function settle() {
			if (lost) {
				lost = "";
				dirty = true;
				return flush().then(() => (lost ? Promise.reject(new Error("The draft did not save: " + lost)) : settle()));
			}
			if (!dirty && !saving) return Promise.resolve();
			return flush().then(settle);
		}
		window.addEventListener("beforeunload", (e) => {
			if (dirty || saving) { e.preventDefault(); e.returnValue = ""; }
		});

		// renderHead shows the validity and the draft state as marker and word.
		function renderHead() {
			const n = check.problems.length, v = $("validity"), s = $("saved");
			v.textContent = check.valid ? "[+] valid" : "[!] " + plural(n, "problem");
			v.className = "validity " + (check.valid ? "ok" : "bad");
			s.className = "saved" + (lost ? " bad" : "");
			if (lost) s.textContent = "[!] not saved";
			else if (dirty || saving) s.textContent = "[>] saving";
			else if (savedAt) {
				const sec = Math.round((Date.now() - savedAt) / 1000);
				s.textContent = "saved " + (sec < 5 ? "just now" : sec < 60 ? sec + "s ago" : Math.round(sec / 60) + "m ago");
			} else s.textContent = "saved at " + boot.saved;
			s.title = lost;
		}
		setInterval(renderHead, 5000);
		function say(text, state) {
			clearTimeout(sayTimer);
			$("status").textContent = text;
			$("status").dataset.state = state || "";
			sayTimer = setTimeout(() => { $("status").textContent = ""; }, 6000);
		}

		// Canvas.
		function subtitle(n) {
			const t = types[n.type];
			let s = n.type === n.name && t ? t.kind : n.type || "(no type)";
			if (facts(n.type).mf) s += " · model-free";
			return s;
		}
		function label(n, probs) {
			if (probs) return "[!] " + plural(probs, "problem");
			const skipped = n.tiers && n.tiers.length ? TIERS.filter((t) => n.tiers.indexOf(t) < 0) : [];
			return skipped.length ? "skipped at " + skipped.join(", ") : "every tier";
		}
		// cut keeps a line inside the box, with the viewer's caps.
		const cut = (s, n) => { const r = Array.from(s || ""); return r.length > n ? r.slice(0, n - 1).join("") + "…" : s || ""; };
		const nodeProblems = (i) => check.problems.filter((p) => p.index === i);
		const card = (i) => nodesG.children[i] || null;
		const place = (g, p) => g.setAttribute("transform", "translate(" + p.x + " " + p.y + ")");

		function render() {
			const active = document.activeElement;
			let refocus = active && active.classList && active.classList.contains("node") ? Number(active.dataset.i) : -1;
			// Keep the focus on a node only while it is still the selection.
			if (!(link || (sel && sel.k === "node" && sel.i === refocus))) refocus = -1;
			nodesG.textContent = "";
			doc.nodes.forEach((n, i) => {
				const probs = nodeProblems(i).length;
				const name = n.name || "(no name)";
				const g = svgEl("g", { class: "node s-" + (probs ? "failed" : "static") + (sel && sel.k === "node" && sel.i === i ? " selected" : "") + (link && link.target === i ? " target" : ""), tabindex: "0", role: "button", "data-i": i });
				g.setAttribute("aria-label", name + ", " + subtitle(n) + ", " + label(n, probs));
				const title = svgEl("title");
				title.textContent = name + "\n" + subtitle(n) + "\n" + label(n, probs);
				g.appendChild(title);
				g.appendChild(svgEl("rect", { class: "ring", x: -5, y: -5, width: NW + 10, height: NH + 10, rx: 12 }));
				g.appendChild(svgEl("rect", { class: "box", width: NW, height: NH, rx: 8 }));
				[["n-name", 24, cut(name, 22)], ["n-sub", 41, cut(subtitle(n), 26)], ["n-status", 60, cut(label(n, probs), 26)], ["n-meta", 76, cut(n.model && n.model.model, 26)]].forEach((t) => {
					const x = svgEl("text", { class: t[0], x: 14, y: t[1] });
					x.textContent = t[2];
					g.appendChild(x);
				});
						const port = svgEl("g", { class: "handle port" });
				port.appendChild(svgEl("circle", { cx: NW / 2 - 14, cy: NH, r: 6 }));
				const add = svgEl("g", { class: "handle add" });
				add.appendChild(svgEl("circle", { cx: NW / 2 + 14, cy: NH, r: 9 }));
				const plus = svgEl("text", { x: NW / 2 + 14, y: NH + 5 });
				plus.textContent = "+";
				add.appendChild(plus);
				g.appendChild(port);
				g.appendChild(add);
				place(g, pos[i]);
				nodesG.appendChild(g);
			});
			drawEdges();
			sizePlane();
			$("empty").hidden = doc.nodes.length > 0;
			$("title").textContent = doc.name || "(no name)";
			document.title = (doc.name || "Draft") + " · Pipeline editor · Splice";
			renderProblems();
			renderHead();
			if (tab === "json") showJSON();
			if (refocus >= 0 && card(refocus)) {
				rendering = true;
				card(refocus).focus({ preventScroll: true });
				rendering = false;
			}
		}
		let planeW = 0, planeH = 0;
		function sizePlane() {
			let w = 0, h = 0;
			pos.forEach((p) => { w = Math.max(w, p.x + NW); h = Math.max(h, p.y + NH); });
			planeW = Math.max(w + 48, (wrap.clientWidth - 2) / zoom);
			planeH = Math.max(h + 64, (wrap.clientHeight - 2) / zoom);
			svg.setAttribute("viewBox", "0 0 " + planeW + " " + planeH);
			svg.setAttribute("width", planeW * zoom);
			svg.setAttribute("height", planeH * zoom);
		}
		function edgePath(a, b) {
			const x1 = a.x + NW / 2, y1 = a.y + NH, x2 = b.x + NW / 2, y2 = b.y;
			const bend = Math.max(Math.abs(y2 - y1) / 2, 40);
			return "M" + x1 + " " + y1 + " C" + x1 + " " + (y1 + bend) + " " + x2 + " " + (y2 - bend) + " " + x2 + " " + y2;
		}
		function drawEdges() {
			edgesG.textContent = "";
			doc.edges.forEach((e, i) => {
				const a = indexOfName(e.from), b = indexOfName(e.to);
				if (a < 0 || b < 0) return;
				const d = edgePath(pos[a], pos[b]);
				edgesG.appendChild(svgEl("path", { d: d, "marker-end": "url(#arrow)", class: "edge " + (e.payload || "summary") + (sel && sel.k === "edge" && sel.i === i ? " sel" : "") + (check.problems.some((p) => p.edge === i) ? " badedge" : "") }));
				edgesG.appendChild(svgEl("path", { d: d, class: "hit", "data-edge": i }));
				if (e.payload && e.payload !== "summary") {
					const t = svgEl("text", { x: (pos[a].x + pos[b].x) / 2 + NW / 2 + 6, y: (pos[a].y + NH + pos[b].y) / 2 });
					t.textContent = e.payload;
					edgesG.appendChild(t);
				}
			});
		}
		function setZoom(z) {
			zoom = Math.min(Math.max(z, 0.3), 2);
			sizePlane();
			say("Zoom " + Math.round(zoom * 100) + "%.");
		}
		function fit() {
			let w = 0, h = 0;
			pos.forEach((p) => { w = Math.max(w, p.x + NW + PAD); h = Math.max(h, p.y + NH + PAD); });
			if (w && h) setZoom(Math.min(wrap.clientWidth / w, wrap.clientHeight / h, 1));
			wrap.scrollTo(0, 0);
		}
		function relayout() {
			api("POST", "layout", { doc: doc })
				.then((d) => say(commit(() => { pos = d.positions; }) ? "Laid out as the viewer draws it. Press u to undo." : "The graph already has the viewer layout."))
				.catch((err) => say(err.message, "error"));
		}

		// Selection and panels.
		// focus selects, so an action that moves the focus decides the selection.
		function select(s, focus) {
			sel = s;
			if (link && !(s && s.k === "node")) endLink();
			document.body.classList.toggle("sheet", !!s);
			render();
			fillPanel();
			if (focus && s && s.k === "node" && card(s.i)) {
				card(s.i).focus();
				card(s.i).scrollIntoView({ block: "nearest", inline: "nearest" });
			}
		}
		// inspected is the selected node, or the target of the selected edge.
		function inspected() {
			if (!sel) return -1;
			const e = sel.k === "edge" && doc.edges[sel.i];
			return sel.k === "node" ? sel.i : e ? indexOfName(e.to) : -1;
		}
		const setVal = (id, v) => { $(id).value = v === undefined || v === null ? "" : String(v); };

		function fillPanel() {
			$("drop-note").hidden = true;
			const i = inspected();
			topoForm.hidden = i >= 0;
			nodeForm.hidden = i < 0;
			if (i < 0) {
				$("panel-title").textContent = "Pipeline";
				setVal("t-name", doc.name);
				setVal("t-desc", doc.description);
				setVal("t-min", doc.splice_min_version);
				setVal("t-in", doc.budget && doc.budget.total_input);
				setVal("t-out", doc.budget && doc.budget.total_output);
			} else {
				const n = doc.nodes[i];
				$("panel-title").textContent = n.name || "(no name)";
				setVal("n-name", n.name);
				setVal("n-type", n.type);
				const m = n.model || {};
				setVal("n-provider", m.provider_profile);
				setVal("n-model", m.model);
				setVal("n-effort", m.reasoning_effort);
				modelOptions();
				nodeForm.querySelectorAll("[data-tier]").forEach((c) => { c.checked = (n.tiers || []).indexOf(c.dataset.tier) >= 0; });
				setVal("n-in", n.budget && n.budget.input_max);
				setVal("n-out", n.budget && n.budget.output_max);
				setVal("n-prompt", n.prompt);
				nodeForm.querySelectorAll("[data-tool]").forEach((c) => { c.checked = (n.tools || []).indexOf(c.dataset.tool) >= 0; });
				setVal("n-turns", n.max_turns);
				setVal("n-perm", n.permission);
				const caps = n.capabilities || {};
				nodeForm.querySelectorAll("select[data-cap]").forEach((s) => { s.value = caps[s.dataset.cap] === undefined ? "" : String(caps[s.dataset.cap]); });
				nodeForm.querySelector("input[data-cap]").checked = !!caps.produces_verification;
				fillArgv(n);
				applyType(n);
				fillDeps(i);
			}
			showErrors();
		}
		function applyType(n) {
			const f = facts(n.type);
			nodeForm.querySelectorAll("[data-show]").forEach((x) => { x.hidden = !f[x.dataset.show]; });
			$("type-hint").textContent = (types[n.type] || {}).about || "";
			$("budget-hint").textContent = n.type === "agent" ? "Agent nodes need both caps. They add to the tier envelope." : "Empty: the tier budget table.";
			$("perm-rule").textContent = rules[n.permission || ""] || "";
		}
		function fillArgv(n) {
			const list = $("argv");
			list.textContent = "";
			(n.command || []).forEach((arg, k) => {
				const li = el("li");
				const input = el("input");
				input.value = arg;
				input.dataset.argv = String(k);
				input.spellcheck = false;
				input.setAttribute("aria-label", "Argument " + (k + 1));
				li.appendChild(input);
				[["↑", "up", "up"], ["↓", "down", "down"], ["✕", "rm", "remove"]].forEach((b) => {
					const btn = el("button", "", b[0]);
					btn.type = "button";
					btn.dataset.argvOp = b[1];
					btn.dataset.argvAt = String(k);
					btn.setAttribute("aria-label", "Argument " + (k + 1) + ": " + b[2]);
					li.appendChild(btn);
				});
				list.appendChild(li);
			});
			argvLine();
		}
		// argvLine shows the server's command line, never an old one.
		function argvLine() {
			const i = inspected(), n = i >= 0 ? doc.nodes[i] : null;
			if (!n) return;
			const known = check.commands.filter((c) => c.name === n.name)[0];
			$("argv-line").textContent = !n.command || !n.command.length ? "(no command yet)" : dirty || saving || !known ? "(checking)" : known.line;
		}
		// fillDeps lists the upstream nodes: the keyboard path for wiring. The
		// add list leaves out every node that makes a cycle.
		function fillDeps(i) {
			const n = doc.nodes[i], list = $("deps"), add = $("dep-add");
			list.textContent = "";
			doc.edges.forEach((e, k) => {
				if (e.to !== n.name) return;
				const li = el("li", sel.k === "edge" && sel.i === k ? "hl" : "");
				li.appendChild(el("span", "mono", e.from));
				const s = el("select");
				s.dataset.dep = String(k);
				s.setAttribute("aria-label", "Payload from " + e.from);
				[["", "summary"], ["output", "output"], ["none", "none: order only"]].forEach((o) => {
					const opt = el("option", "", o[1]);
					opt.value = o[0];
					s.appendChild(opt);
				});
				s.value = e.payload === "summary" ? "" : e.payload || "";
				const rm = el("button", "", "✕");
				rm.type = "button";
				rm.dataset.depRm = String(k);
				rm.setAttribute("aria-label", "Remove the dependency on " + e.from);
				li.appendChild(s);
				li.appendChild(rm);
				list.appendChild(li);
			});
			if (!list.children.length) list.appendChild(el("li", "muted", "Nothing. It runs first."));
			add.textContent = "";
			add.appendChild(el("option", "", "Add a dependency…")).value = "";
			doc.nodes.forEach((o, j) => {
				if (j === i || doc.edges.some((e) => e.from === o.name && e.to === n.name) || pathBetween(n.name, o.name)) return;
				add.appendChild(el("option", "", o.name)).value = String(j);
			});
			const feeds = doc.edges.filter((e) => e.from === n.name).map((e) => e.to);
			$("feeds").textContent = feeds.length ? "Feeds " + feeds.join(", ") + "." : "No node depends on it.";
		}
		function modelOptions() {
			const provider = $("n-provider").value.trim(), list = $("models");
			list.textContent = "";
			boot.models.forEach((m) => {
				if (provider && m.provider !== provider) return;
				const o = el("option");
				o.value = m.model;
				o.label = m.provider;
				list.appendChild(o);
			});
		}

		// showErrors puts each problem next to its field, else at the form top.
		function showErrors() {
			panel.querySelectorAll("[data-err]").forEach((x) => { x.textContent = ""; });
			const i = inspected();
			const form = i < 0 ? topoForm : nodeForm;
			const mine = check.problems.filter((p) => (i < 0 ? p.index === undefined && p.edge === undefined : p.index === i || (p.edge !== undefined && doc.edges[p.edge] && doc.edges[p.edge].to === doc.nodes[i].name)));
			mine.forEach((p) => {
				let slot = p.field && form.querySelector('[data-err="' + p.field + '"]');
				if (!slot || slot.closest("[hidden]")) slot = form.querySelector('[data-err="_node"]') || panel.querySelector('[data-err="_"]');
				const words = (p.tier ? "At tier " + p.tier + ": " : "") + p.message;
				slot.textContent = slot.textContent ? slot.textContent + "\n" + words : words;
			});
			argvLine();
		}
		function renderProblems() {
			const list = $("problems");
			list.textContent = "";
			check.problems.forEach((p) => {
				const e = p.edge !== undefined && doc.edges[p.edge];
				const where = p.index !== undefined ? "node " + (p.node || "#" + (p.index + 1)) : e ? "edge " + e.from + " → " + e.to : "pipeline";
				const b = el("button", "", "[!] " + where + (p.field ? " · " + p.field : "") + (p.tier ? " · at tier " + p.tier : "") + ": " + p.message);
				b.type = "button";
				b.addEventListener("click", () => goTo(p));
				list.appendChild(el("li")).appendChild(b);
			});
			const warn = $("warnings");
			warn.textContent = "";
			(check.warnings || []).forEach((w) => warn.appendChild(el("li", "", "[~] " + w)));
		}
		function goTo(p) {
			if (p.index !== undefined && p.index < doc.nodes.length) select({ k: "node", i: p.index }, false);
			else if (p.edge !== undefined && p.edge < doc.edges.length) select({ k: "edge", i: p.edge }, false);
			else { select(null, false); document.body.classList.add("sheet"); }
			showTab("form");
			const slot = p.field && panel.querySelector('form:not([hidden]) [data-err="' + p.field + '"]');
			const box = slot && slot.closest(".f");
			const target = (box && box.querySelector("input, select, textarea, button")) || panel.querySelector("form:not([hidden]) input, form:not([hidden]) select");
			if (target) target.focus();
		}
		function showTab(name) {
			tab = name;
			$("tab-form").setAttribute("aria-selected", String(name === "form"));
			$("tab-json").setAttribute("aria-selected", String(name === "json"));
			$("forms").hidden = name !== "form";
			$("json-pane").hidden = name !== "json";
			if (name === "json") showJSON();
		}
		// showJSON shows the exact file that publish writes, from the server.
		function showJSON() {
			$("json-note").textContent = check.document ? "Publish writes exactly this file. It is read-only: edit with the form." : "The draft has problems, so publish writes nothing yet. This is the draft as it is saved.";
			$("json").textContent = check.document || JSON.stringify(doc, null, 2);
		}

		// Edits.
		function openPalette(from) {
			after = from;
			document.body.classList.add("palopen");
			$("pal-hint").textContent = from >= 0 ? "Choose a type. The node goes after " + doc.nodes[from].name + "." : "Choose a type to add a node.";
			$("pal-search").focus();
		}
		function closePalette() {
			after = -1;
			$("pal-search").value = "";
			document.querySelectorAll(".pal li").forEach((li) => { li.hidden = false; });
			$("pal-empty").hidden = true;
			document.body.classList.remove("palopen");
			$("pal-hint").textContent = "Choose a type to add a node.";
		}
		function addNode(type) {
			const base = types[type] && types[type].builtin ? type : type + "-1";
			let name = base, k = 2;
			while (indexOfName(name) >= 0) name = (types[type] && types[type].builtin ? type + "-" : type + "-") + k++;
			const from = after;
			const anchor = from >= 0 ? pos[from] : sel && sel.k === "node" ? pos[sel.i] : pos.reduce((low, p) => (p.y > low.y ? p : low), { x: PAD, y: PAD - NH - GAP });
			const node = { name: name, type: type };
			if (facts(type).prompt) node.prompt = "";
			commit(() => {
				// Inside the change, so undo takes the version back too.
				if (type === "agent" && !doc.splice_min_version) doc.splice_min_version = boot.agent_min;
				doc.nodes.push(node);
				pos.push({ x: anchor.x, y: anchor.y + NH + GAP });
				if (from >= 0) doc.edges.push({ from: doc.nodes[from].name, to: name });
			});
			closePalette();
			select({ k: "node", i: doc.nodes.length - 1 }, true);
			say("Added " + name + (from >= 0 ? " after " + doc.nodes[from].name : "") + ". Press Enter to edit it.");
		}
		function removeSel() {
			if (!sel) return;
			if (sel.k === "edge") {
				// Clear the selection in the change: the render after it must not
				// read the removed edge.
				const i = sel.i, e = doc.edges[i], to = indexOfName(e.to);
				commit(() => { doc.edges.splice(i, 1); sel = null; });
				select(to >= 0 ? { k: "node", i: to } : null, false);
				return say("Deleted the edge " + e.from + " → " + e.to + ". Press u to undo.");
			}
			const i = sel.i, n = doc.nodes[i];
			commit(() => {
				if (unique(n.name)) doc.edges = doc.edges.filter((e) => e.from !== n.name && e.to !== n.name);
				doc.nodes.splice(i, 1);
				pos.splice(i, 1);
			});
			say("Deleted " + (n.name || "the node") + " and its edges. Press u to undo.");
			select(doc.nodes.length ? { k: "node", i: Math.min(i, doc.nodes.length - 1) } : null, true);
		}
		function move(dx, dy) {
			const i = sel.i;
			commit(() => { pos[i] = { x: Math.max(0, pos[i].x + dx), y: Math.max(0, pos[i].y + dy) }; }, "mv" + i);
			if (card(i)) card(i).scrollIntoView({ block: "nearest", inline: "nearest" });
		}
		// pathBetween returns the node names from a to b along the edges, or null.
		function pathBetween(a, b, seen) {
			if (a === b) return [b];
			seen = seen || {};
			if (seen[a]) return null;
			seen[a] = true;
			for (const e of doc.edges) {
				if (e.from !== a) continue;
				const rest = pathBetween(e.to, b, seen);
				if (rest) return [a].concat(rest);
			}
			return null;
		}
		function connect(fi, ti) {
			const f = doc.nodes[fi].name, t = doc.nodes[ti].name;
			if (fi === ti) return say("A node cannot depend on itself.", "error");
			if (doc.edges.some((e) => e.from === f && e.to === t)) return say(t + " already depends on " + f + ".", "error");
			const cycle = pathBetween(t, f);
			if (cycle) return say("That edge makes a cycle: " + [f].concat(cycle).join(" → ") + ". A pipeline cannot have cycles.", "error");
			commit(() => { doc.edges.push({ from: f, to: t }); });
			if (inspected() >= 0) fillDeps(inspected());
			say("Connected " + f + " → " + t + ". The edge passes the summary.");
		}
		function startLink() {
			if (!sel || sel.k !== "node") return say("Select a node first, then press e.", "error");
			if (doc.nodes.length < 2) return say("Add another node first.", "error");
			link = { from: sel.i, target: (sel.i + 1) % doc.nodes.length };
			document.body.classList.add("linking");
			render();
			card(link.target).focus();
			say("Connect from " + doc.nodes[link.from].name + ": choose the target with Tab or the arrow keys, then press Enter. Esc cancels.");
		}
		function stepTarget(d) {
			const n = doc.nodes.length;
			let t = link.target;
			do { t = (t + d + n) % n; } while (t === link.from);
			link.target = t;
			render();
			card(t).focus();
		}
		function endLink() {
			link = null;
			document.body.classList.remove("linking");
		}
		function finishLink(t) {
			const from = link.from;
			endLink();
			connect(from, t);
			select({ k: "node", i: from }, true);
		}
		function changeType(n, type) {
			const old = n.type, f = facts(type), dropped = [];
			n.type = type;
			const drop = (obj, k, keep) => { if (obj && !keep && obj[k] !== undefined) { delete obj[k]; dropped.push(k); } };
			drop(n, "prompt", f.prompt);
			drop(n, "command", f.command);
			drop(n, "tools", f.agent);
			drop(n, "max_turns", f.agent);
			drop(n, "permission", f.agent);
			drop(n, "model", f.model);
			drop(n, "budget", f.budget);
			drop(n.capabilities, "model_free", f.mf);
			drop(n.capabilities, "pull_context", f.pullctx);
			drop(n.capabilities, "pull_memory", f.pullmem);
			drop(n.capabilities, "produces_verification", f.verif);
			let note = "Changed the type from " + old + " to " + type + ". " + (dropped.length ? "Dropped the fields that do not apply: " + dropped.join(", ") + "." : "Every field still applies.");
			if (type === "agent" && !doc.splice_min_version) {
				doc.splice_min_version = boot.agent_min;
				note += " Set the minimum Splice version to " + boot.agent_min + ", which agent nodes need.";
			}
			return note;
		}
		function numberOrNone(v) {
			const t = String(v).trim();
			return t === "" ? undefined : Number(t);
		}
		function setOrDelete(obj, k, v) {
			if (v === undefined || v === "") delete obj[k];
			else obj[k] = v;
		}
		const checked = (sel2, k) => Array.prototype.filter.call(nodeForm.querySelectorAll(sel2), (c) => c.checked).map((c) => c.dataset[k]);

		nodeForm.addEventListener("input", (e) => {
			const t = e.target, i = inspected(), n = doc.nodes[i];
			const key = "n" + i + (t.id || t.dataset.tier || t.dataset.tool || t.dataset.cap || "a" + t.dataset.argv);
			if (t.dataset.tier !== undefined) {
				commit(() => { const v = checked("[data-tier]", "tier"); setOrDelete(n, "tiers", v.length ? v : undefined); });
			} else if (t.dataset.tool !== undefined) {
				commit(() => { const v = checked("[data-tool]", "tool"); setOrDelete(n, "tools", v.length ? v : undefined); });
			} else if (t.dataset.cap !== undefined) {
				commit(() => {
					const caps = n.capabilities || {};
					setOrDelete(caps, t.dataset.cap, t.type === "checkbox" ? (t.checked ? true : undefined) : t.value === "" ? undefined : t.value === "true");
					setOrDelete(n, "capabilities", Object.keys(caps).length ? caps : undefined);
				});
			} else if (t.dataset.argv !== undefined) {
				commit(() => { n.command[Number(t.dataset.argv)] = t.value; }, key);
			} else if (t.dataset.dep !== undefined) {
				commit(() => { setOrDelete(doc.edges[Number(t.dataset.dep)], "payload", t.value); });
			} else if (t.id === "dep-add") {
				if (t.value !== "") connect(Number(t.value), i);
			} else {
				switch (t.dataset.k) {
					case "name": {
						const old = n.name;
						commit(() => {
							if (unique(old)) doc.edges.forEach((ed) => { if (ed.from === old) ed.from = t.value; if (ed.to === old) ed.to = t.value; });
							n.name = t.value;
						}, key);
						$("panel-title").textContent = t.value || "(no name)";
						break;
					}
					case "type": {
						let note = "";
						commit(() => { note = changeType(n, t.value); });
						fillPanel();
						$("drop-note").hidden = false;
						$("drop-note").textContent = note;
						break;
					}
					case "provider":
					case "model":
					case "effort":
						commit(() => {
							const m = { provider_profile: $("n-provider").value.trim(), model: $("n-model").value.trim() };
							if ($("n-effort").value) m.reasoning_effort = $("n-effort").value;
							setOrDelete(n, "model", m.provider_profile || m.model || m.reasoning_effort ? m : undefined);
						}, key);
						if (t.dataset.k === "provider") modelOptions();
						break;
					case "input_max":
					case "output_max":
						commit(() => {
							const a = numberOrNone($("n-in").value), b = numberOrNone($("n-out").value);
							setOrDelete(n, "budget", a === undefined && b === undefined ? undefined : Object.assign({}, n.budget || {}, { input_max: a || 0, output_max: b || 0 }));
						}, key);
						break;
					case "max_turns":
						commit(() => { setOrDelete(n, "max_turns", numberOrNone(t.value)); }, key);
						break;
					default:
						commit(() => { setOrDelete(n, t.dataset.k, t.value); }, key);
						if (t.dataset.k === "permission") applyType(n);
				}
			}
		});
		nodeForm.addEventListener("click", (e) => {
			const b = e.target.closest("button");
			if (!b) return;
			const n = doc.nodes[inspected()];
			if (b.id === "argv-add") {
				commit(() => { n.command = (n.command || []).concat([""]); });
				fillArgv(n);
				const inputs = $("argv").querySelectorAll("input");
				inputs[inputs.length - 1].focus();
			} else if (b.dataset.argvOp) {
				const k = Number(b.dataset.argvAt), op = b.dataset.argvOp;
				commit(() => {
					const c = n.command;
					if (op === "rm") c.splice(k, 1);
					else {
						const j = op === "up" ? k - 1 : k + 1;
						if (j < 0 || j >= c.length) return;
						const v = c[k]; c[k] = c[j]; c[j] = v;
					}
					if (!c.length) delete n.command;
				});
				fillArgv(n);
			} else if (b.dataset.depRm !== undefined) {
				const k = Number(b.dataset.depRm), e = doc.edges[k];
				commit(() => { doc.edges.splice(k, 1); sel = { k: "node", i: indexOfName(n.name) }; });
				fillDeps(inspected());
				$("dep-add").focus();
				say("Removed the dependency on " + e.from + ". Press u to undo.");
			} else if (b.id === "node-delete") removeSel();
		});
		topoForm.addEventListener("input", (e) => {
			const t = e.target;
			commit(() => {
				if (t.dataset.k === "total_input" || t.dataset.k === "total_output") {
					const a = numberOrNone($("t-in").value), b = numberOrNone($("t-out").value);
					setOrDelete(doc, "budget", a === undefined && b === undefined ? undefined : { total_input: a || 0, total_output: b || 0 });
				} else setOrDelete(doc, t.dataset.k, t.value);
			}, "t" + t.id);
		});
		[topoForm, nodeForm].forEach((f) => f.addEventListener("submit", (e) => e.preventDefault()));

		// Palette.
		const palButtons = () => Array.prototype.filter.call(document.querySelectorAll(".pal button"), (b) => !b.parentNode.hidden);
		$("pal-search").addEventListener("input", (e) => {
			const q = e.target.value.trim().toLowerCase();
			document.querySelectorAll(".pal button").forEach((b) => { b.parentNode.hidden = !!q && b.textContent.toLowerCase().indexOf(q) < 0; });
			$("pal-empty").hidden = palButtons().length > 0;
		});
		$("pal-search").addEventListener("keydown", (e) => {
			const first = palButtons()[0];
			if (e.key === "Enter" && first && e.target.value.trim()) { e.preventDefault(); addNode(first.dataset.type); }
			if (e.key === "ArrowDown" && first) { e.preventDefault(); first.focus(); }
		});
		$("palette").addEventListener("click", (e) => {
			const b = e.target.closest("button[data-type]");
			if (b) addNode(b.dataset.type);
		});
		$("palette").addEventListener("keydown", (e) => {
			if ((e.key !== "ArrowDown" && e.key !== "ArrowUp") || !e.target.dataset.type) return;
			const all = palButtons(), k = all.indexOf(e.target) + (e.key === "ArrowDown" ? 1 : -1);
			e.preventDefault();
			if (k < 0) $("pal-search").focus();
			else if (all[k]) all[k].focus();
		});

		// Pointer.
		function planePoint(e) {
			const r = svg.getBoundingClientRect();
			return { x: (e.clientX - r.left) / zoom, y: (e.clientY - r.top) / zoom };
		}
		nodesG.addEventListener("pointerdown", (e) => {
			const g = e.target.closest(".node");
			if (!g || e.button !== 0) return;
			const i = Number(g.dataset.i);
			e.preventDefault();
			if (link) return finishLink(i);
			if (e.target.closest(".add")) return openPalette(i);
			if (e.target.closest(".port")) {
				drag = { link: i };
				g.setPointerCapture(e.pointerId);
				return;
			}
			if (!(sel && sel.k === "node" && sel.i === i)) select({ k: "node", i: i }, false);
			const live = card(i);
			live.focus({ preventScroll: true });
			drag = { i: i, el: live, sx: e.clientX, sy: e.clientY, ox: pos[i].x, oy: pos[i].y, moved: false };
			live.setPointerCapture(e.pointerId);
		});
		// endDrag puts a dragged node back. A move with no button ends a drag
		// whose pointerup was missed, so a hover never moves a node.
		function endDrag() {
			if (drag && drag.el) {
				pos[drag.i] = { x: drag.ox, y: drag.oy };
				place(drag.el, pos[drag.i]);
				drawEdges();
			}
			rubber.setAttribute("d", "");
			drag = null;
		}
		document.addEventListener("pointercancel", endDrag);
		document.addEventListener("pointermove", (e) => {
			if (!drag) return;
			if (!e.buttons) return endDrag();
			if (drag.link !== undefined) {
				const a = pos[drag.link], p = planePoint(e);
				rubber.setAttribute("d", "M" + (a.x + NW / 2 - 14) + " " + (a.y + NH) + " L" + p.x + " " + p.y);
				return;
			}
			const dx = (e.clientX - drag.sx) / zoom, dy = (e.clientY - drag.sy) / zoom;
			if (!drag.moved && Math.abs(dx) + Math.abs(dy) < 4) return;
			drag.moved = true;
			drag.nx = Math.max(0, drag.ox + dx);
			drag.ny = Math.max(0, drag.oy + dy);
			drag.el.classList.add("dragging");
			if (!raf) raf = requestAnimationFrame(frame);
		});
		// frame moves the dragged node by its transform, once per animation frame.
		function frame() {
			raf = 0;
			if (!drag || drag.link !== undefined) return;
			pos[drag.i] = { x: drag.nx, y: drag.ny };
			place(drag.el, pos[drag.i]);
			drawEdges();
		}
		document.addEventListener("pointerup", (e) => {
			const d = drag;
			drag = null;
			if (!d) return;
			if (d.link !== undefined) {
				rubber.setAttribute("d", "");
				const under = document.elementFromPoint(e.clientX, e.clientY);
				const target = under && under.closest(".node");
				if (target) connect(d.link, Number(target.dataset.i));
				return;
			}
			if (!d.moved) return;
			if (raf) { cancelAnimationFrame(raf); raf = 0; }
			const final = { x: d.nx, y: d.ny };
			pos[d.i] = { x: d.ox, y: d.oy };
			commit(() => { pos[d.i] = final; });
		});
		wrap.addEventListener("click", (e) => {
			const hit = e.target.closest("[data-edge]");
			if (hit) return select({ k: "edge", i: Number(hit.dataset.edge) }, false);
			if (!e.target.closest(".node")) select(null, false);
		});
		nodesG.addEventListener("focusin", (e) => {
			const g = e.target.closest(".node");
			if (!g || rendering) return;
			const i = Number(g.dataset.i);
			if (link) { if (i !== link.from && link.target !== i) { link.target = i; render(); } return; }
			if (!(sel && sel.k === "node" && sel.i === i)) select({ k: "node", i: i }, false);
		});
		window.addEventListener("resize", sizePlane);
		wrap.tabIndex = -1;

		// Keyboard.
		document.addEventListener("keydown", (e) => {
			if (dialog.open || $("keys").open) return;
			const t = e.target, typing = t.matches && t.matches("input, textarea, select");
			if (e.key === "Escape") {
				e.preventDefault();
				if (typing && t.id !== "pal-search") { t.blur(); if (sel && sel.k === "node" && card(sel.i)) card(sel.i).focus(); return; }
				if (document.body.classList.contains("palopen")) { closePalette(); wrap.focus({ preventScroll: true }); return; }
				if (link) { const from = link.from; endLink(); say("Connect cancelled."); return select({ k: "node", i: from }, true); }
				// Focus selects, so the canvas frame takes the focus after a deselect.
				if (sel) { select(null, false); wrap.focus({ preventScroll: true }); return; }
				document.body.classList.remove("sheet");
				return;
			}
			if (typing) return;
			const mod = e.ctrlKey || e.metaKey;
			if (mod && (e.key === "z" || e.key === "Z")) { e.preventDefault(); return e.shiftKey ? travel(redo, undo, "redo") : travel(undo, redo, "undo"); }
			if (mod && e.key === "y") { e.preventDefault(); return travel(redo, undo, "redo"); }
			if (mod || e.altKey) return;
			if (t.tagName === "BUTTON" && (e.key === "Enter" || e.key === " ")) return;
			// Selection keys work only on the canvas, never on a button or a panel.
			const onCanvas = t === document.body || !!t.closest("#wrap");
			if (!onCanvas && ["Delete", "Backspace", "Enter", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].indexOf(e.key) >= 0) return;
			const step = e.shiftKey ? 32 : 8;
			switch (e.key) {
				case "n": openPalette(-1); break;
				case "a": if (sel && sel.k === "node") openPalette(sel.i); else return; break;
				case "e": startLink(); break;
				case "u": travel(undo, redo, "undo"); break;
				case "U": travel(redo, undo, "redo"); break;
				case "p": publish(); break;
				case "s": select(null, false); showTab("form"); document.body.classList.add("sheet"); $("t-name").focus(); break;
				case "j": document.body.classList.add("sheet"); showTab("json"); $("json").focus(); break;
				case "?": $("keys").showModal(); break;
				case "+": case "=": setZoom(zoom * 1.2); break;
				case "-": setZoom(zoom / 1.2); break;
				case "0": setZoom(1); break;
				case "f": fit(); break;
				case "l": relayout(); break;
				case "Delete":
				case "Backspace": removeSel(); break;
				case "Enter":
					if (link) finishLink(link.target);
					else if (sel) { document.body.classList.add("sheet"); showTab("form"); (sel.k === "node" ? $("n-name") : $("deps").querySelector("li.hl select") || $("dep-add")).focus(); }
					else return;
					break;
				case "ArrowLeft":
				case "ArrowRight":
				case "ArrowUp":
				case "ArrowDown":
					if (link) { stepTarget(e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 1); break; }
					if (!sel || sel.k !== "node") return;
					move(e.key === "ArrowLeft" ? -step : e.key === "ArrowRight" ? step : 0, e.key === "ArrowUp" ? -step : e.key === "ArrowDown" ? step : 0);
					break;
				default: return;
			}
			e.preventDefault();
		});
		const on = (id, fn) => $(id).addEventListener("click", fn);
		on("nodes-open", () => openPalette(-1));
		on("undo", () => travel(undo, redo, "undo"));
		on("redo", () => travel(redo, undo, "redo"));
		on("settings", () => { select(null, false); showTab("form"); document.body.classList.add("sheet"); $("t-name").focus(); });
		on("validate", () => {
			dirty = true;
			settle().then(() => {
				say(check.valid ? "[+] Checked: valid, ready to publish." : "[!] Checked: " + plural(check.problems.length, "problem") + ".", check.valid ? "" : "error");
				const first = $("problems").querySelector("button");
				if (first) first.focus();
			}).catch((err) => say(err.message, "error"));
		});
		on("z-in", () => setZoom(zoom * 1.2));
		on("z-out", () => setZoom(zoom / 1.2));
		on("z-fit", fit);
		on("z-reset", () => { setZoom(1); wrap.scrollTo(0, 0); });
		on("relayout", relayout);
		on("help", () => $("keys").showModal());
		on("keys-close", () => $("keys").close());
		on("tab-form", () => showTab("form"));
		on("tab-json", () => showTab("json"));
		on("json-copy", () => {
			navigator.clipboard.writeText($("json").textContent).then(() => say("Copied the JSON."), () => say("The browser did not allow the copy. Select the text and copy it.", "error"));
		});
		document.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", () => {
			closePalette();
			select(null, false);
			document.body.classList.remove("sheet");
		}));

		// Publish.
		const pubBody = $("pub-body"), pubErr = $("pub-error"), pubOk = $("pub-ok"), pubUse = $("pub-use"), pubCancel = $("pub-cancel");
		function section(title, items, fill) {
			pubBody.appendChild(el("h3", "", title));
			if (!items.length) return;
			const ul = el("ul");
			items.forEach((x) => fill(ul.appendChild(el("li")), x));
			pubBody.appendChild(ul);
		}
		function showConsent(c) {
			consent = c;
			pubBody.textContent = "";
			pubErr.textContent = "";
			$("pub-title").textContent = (c.replaces ? "Replace " : "Publish ") + c.name;
			pubBody.appendChild(el("p", c.replaces ? "replace" : "", c.replaces ? "[~] A library entry named " + c.name + " exists. Publish replaces it." : "Publish installs a new library entry."));
			pubBody.appendChild(el("p", "muted", "File: ")).appendChild(el("code", "", c.path));
			if (c.replaces) {
				if (c.diff_note) pubBody.appendChild(el("p", "err", c.diff_note));
				if (c.diff) {
					const d = c.diff;
					const rows = [["Nodes added", d.nodes_added], ["Nodes removed", d.nodes_removed], ["Nodes changed", d.nodes_changed], ["Edges added", d.edges_added], ["Edges removed", d.edges_removed], ["Edges changed", d.edges_changed], ["Settings changed", d.settings]].filter((r) => r[1].length);
					section("Changes from the current entry", rows, (li, r) => { li.appendChild(el("strong", "", r[0] + ": ")); li.appendChild(el("span", "mono", r[1].join(", "))); });
					if (!rows.length) pubBody.appendChild(el("p", "muted", "No structural change."));
				}
			}
			section("Command nodes", c.commands, (li, x) => { li.appendChild(el("span", "command-name", x.name + " ")); li.appendChild(el("code", "", x.line)); });
			pubBody.appendChild(el("p", "muted", c.commands.length ? c.command_count + ". Each line runs exactly as shown." : "No command nodes."));
			section("Agent nodes", c.agents, (li, a) => {
				li.appendChild(el("span", "command-name", a.name));
				const dl = li.appendChild(el("dl", "agent-facts"));
				[["tools", a.tools], ["permission", a.permission], ["turns", "up to " + a.turns], ["budget", a.budget], ["prompt", a.prompt]].forEach((r) => { dl.appendChild(el("dt", "", r[0])); dl.appendChild(el("dd", "", r[1])); });
				dl.appendChild(el("dt", "", "full prompt"));
				dl.appendChild(el("dd")).appendChild(el("pre", "line full", a.prompt_full)).tabIndex = 0;
			});
			pubBody.appendChild(el("p", "muted", c.agents.length ? c.agent_count + ". " + c.agent_budget.charAt(0).toUpperCase() + c.agent_budget.slice(1) + "." : "No agent nodes."));
			if (c.warnings.length) section("Warnings", c.warnings, (li, w) => { li.textContent = "[~] " + w; });
			pubBody.appendChild(el("p", "muted", "Publish does not change the active pipeline, and a running run keeps its topology."));
			pubOk.hidden = false;
			pubOk.disabled = false;
			pubOk.textContent = c.replaces ? "Replace " + c.name : "Publish " + c.name;
			pubUse.hidden = true;
			pubCancel.textContent = "Cancel";
			if (!dialog.open) dialog.showModal();
			pubCancel.focus();
		}
		function publish() {
			settle()
				.then(() => {
					if (!check.valid) throw new Error("[!] The pipeline has " + plural(check.problems.length, "problem") + ". Fix them before you publish.");
					return api("GET", "consent?id=" + encodeURIComponent(boot.id));
				})
				.then(showConsent)
				.catch((err) => {
					say(err.message, "error");
					const first = $("problems").querySelector("button");
					if (first) first.focus();
				});
		}
		on("publish", publish);
		pubOk.addEventListener("click", () => {
			pubOk.disabled = true;
			pubErr.textContent = "";
			api("POST", "publish?id=" + encodeURIComponent(boot.id), { confirm: true, replace: consent.replaces, digest: consent.digest, old_digest: consent.old_digest || "" })
				.then((d) => {
					pubBody.textContent = "";
					$("pub-title").textContent = "Published " + d.name;
					pubBody.appendChild(el("p", "ok", (d.replaced ? "[+] Replaced the library entry. File: " : "[+] Installed the library entry. File: "))).appendChild(el("code", "", d.path));
					pubBody.appendChild(el("p", "muted", boot.can_use ? "The active pipeline did not change. Choose Use it now to make it active from the next run." : "The active pipeline did not change. To use it, run: splice pipeline use " + d.name));
					pubOk.hidden = true;
					pubUse.hidden = !boot.can_use;
					pubCancel.textContent = "Close";
					(boot.can_use ? pubUse : pubCancel).focus();
				})
				.catch((err) => {
					pubOk.disabled = false;
					pubErr.textContent = err.message;
					if (err.status === 409) api("GET", "consent?id=" + encodeURIComponent(boot.id)).then(showConsent).then(() => { pubErr.textContent = err.message; }).catch(() => {});
				});
		});
		pubUse.addEventListener("click", () => {
			pubUse.disabled = true;
			api("POST", "use", { name: consent.name })
				.then((d) => { pubUse.hidden = true; pubBody.appendChild(el("p", "ok", "[+] " + d.message)); pubCancel.focus(); })
				.catch((err) => { pubErr.textContent = err.message; })
				.then(() => { pubUse.disabled = false; });
		});
		pubCancel.addEventListener("click", () => dialog.close());

		render();
		fillPanel();
		if (!doc.nodes.length) $("pal-search").focus();
	}
})();
