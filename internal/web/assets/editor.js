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
		// The card of the viewer (pipeline_layout.go): the size, the gaps, and the header band.
		const NW = 240, NH = 104, PAD = 12, GAPX = 110, GAPY = 32;
		const BAND = "M1 26 V8 Q1 1 8 1 H232 Q239 1 239 8 V26 Z";
		const PORTY = NH / 2 - 16, ADDY = NH / 2 + 16;
		// Free routing: the route of an edge that has no server path. It has no
		// DOM access. A card box is {x, y}, NW by NH. cubic adds points along a
		// bend to pts; curve is a bend with horizontal ends, its controls c1
		// and c2 units right of its ends. The server bends are c1 = -c2.
		const RM = 8, LANE = 14, LANES = 4;
		function cubic(pts, x1, y1, ax, ay, bx, by, x2, y2) {
			for (let k = 1; k <= 12; k++) {
				const t = k / 12, u = 1 - t;
				pts.push([u * u * u * x1 + 3 * u * u * t * ax + 3 * u * t * t * bx + t * t * t * x2, u * u * u * y1 + 3 * u * u * t * ay + 3 * u * t * t * by + t * t * t * y2]);
			}
		}
		function curve(pts, x1, y1, x2, y2, c1, c2) {
			cubic(pts, x1, y1, x1 + c1, y1, x2 + c2, y2, x2, y2);
			return " C" + (x1 + c1) + " " + y1 + " " + (x2 + c2) + " " + y2 + " " + x2 + " " + y2;
		}
		// pathPoints samples a path of M, C, and H commands, such as a server path.
		function pathPoints(d) {
			const t = d.match(/[MCH]|-?[\d.]+/g).map((v) => (/[MCH]/.test(v) ? v : Number(v))), pts = [];
			for (let k = 0; k < t.length;) {
				const p = pts[pts.length - 1];
				if (t[k] === "M") { pts.push([t[k + 1], t[k + 2]]); k += 3; } else if (t[k] === "H") { pts.push([t[k + 1], p[1]]); k += 2; } else { cubic(pts, p[0], p[1], t[k + 1], t[k + 2], t[k + 3], t[k + 4], t[k + 5], t[k + 6]); k += 7; }
			}
			return pts;
		}
		// hit reports whether the segment a-b crosses the card r grown by g (Liang-Barsky).
		function hit(a, b, r, g) {
			const x0 = r.x - g, y0 = r.y - g, x1 = r.x + NW + g, y1 = r.y + NH + g;
			if (Math.max(a[0], b[0]) < x0 || Math.min(a[0], b[0]) > x1 || Math.max(a[1], b[1]) < y0 || Math.min(a[1], b[1]) > y1) return false;
			const dx = b[0] - a[0], dy = b[1] - a[1];
			let t0 = 0, t1 = 1;
			// Clip the segment by each side of the box: t0 and t1 bound the part inside.
			for (let k = 0; k < 4; k++) {
				const p = k === 0 ? -dx : k === 1 ? dx : k === 2 ? -dy : dy, q = k === 0 ? a[0] - x0 : k === 1 ? x1 - a[0] : k === 2 ? a[1] - y0 : y1 - a[1];
				if (p === 0) { if (q < 0) return false; continue; }
				if (p < 0) t0 = Math.max(t0, q / p); else t1 = Math.min(t1, q / p);
				if (t0 > t1) return false;
			}
			return true;
		}
		// blockers returns the cards, grown by g, that the path pts crosses. A
		// card away from the box of the path is not tested.
		function blockers(pts, rects, g = RM) {
			let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
			pts.forEach((p) => { x0 = Math.min(x0, p[0]); y0 = Math.min(y0, p[1]); x1 = Math.max(x1, p[0]); y1 = Math.max(y1, p[1]); });
			return rects.filter((r) => r.x - g <= x1 && r.x + NW + g >= x0 && r.y - g <= y1 && r.y + NH + g >= y0 && pts.some((p, k) => k > 0 && hit(pts[k - 1], p, r, g)));
		}
		// routeFree routes an edge from the card from to the card to around the
		// cards rects. The plain curve is the first try. When it crosses a card,
		// the edge takes a horizontal lane LANE units above or below the cards in
		// its way (the band), the nearer one first. A forward lane bends in the
		// space between the two cards. A U-turn lane turns right of the band
		// and left of it, and runs above or below the two cards too: a target
		// left of its source gets only U-turn lanes, and never the plain curve,
		// which would cross both cards. A lane that crosses more cards adds them
		// to the band and tries again, LANES times at most for each kind. With
		// no clear lane, it returns the plain curve with fallback set. The label
		// (x, y) starts at the middle of the last lane run, or of the plain
		// curve, and slides along the path to a place off every card.
		function routeFree(from, to, rects) {
			const sx = from.x + NW, sy = from.y + NH / 2, tx = to.x, ty = to.y + NH / 2, s = Math.min(55, (tx - sx) / 2), all = rects.concat([from, to]);
			const done = (r, fallback) => {
				const pts = r.pts, m = r.mid;
				let x = pts[m][0], y = pts[m][1];
				for (let k = 0; k <= pts.length; k++) {
					const p = pts[m + (k % 2 ? (k + 1) / 2 : -k / 2)];
					if (p && !all.some((c) => p[0] + 32 > c.x && p[0] - 32 < c.x + NW && p[1] + 10 > c.y && p[1] - 10 < c.y + NH)) { x = p[0]; y = p[1]; break; }
				}
				return { d: r.d, x: x, y: y, fallback: fallback, box: pts.reduce((b, p) => [Math.min(b[0], p[0]), Math.min(b[1], p[1]), Math.max(b[2], p[0]), Math.max(b[3], p[1])], [x - 32, y - 10, x + 32, y + 10]) };
			};
			const lane = (L, band, fwd) => {
				const east = Math.max(...band.map((r) => r.x + NW)), west = Math.min(...band.map((r) => r.x));
				const pts = [[sx, sy]], xa = fwd ? sx + s : east, xb = fwd ? tx - s : west;
				let d = "M" + sx + " " + sy + (fwd ? curve(pts, sx, sy, xa, L, s / 2, -s / 2) : curve(pts, sx, sy, east, L, east - sx + 40, 40)) + " H" + xb;
				pts.push([(xa + xb) / 2, L], [xb, L]);
				d += fwd ? curve(pts, xb, L, tx, ty, s / 2, -s / 2) : curve(pts, west, L, tx, ty, -40, west - tx - 40);
				return { d: d, pts: pts, mid: 13 };
			};
			const pts = [[sx, sy]], c = Math.max(Math.abs(tx - sx) / 2, 40), plain = { d: "M" + sx + " " + sy + curve(pts, sx, sy, tx, ty, c, -c), pts: pts, mid: 6 };
			const first = tx < sx ? [] : blockers(pts, rects);
			if (tx >= sx && !first.length) return done(plain, false);
			for (const fwd of tx - sx >= 40 ? [true, false] : [false]) {
				let block = first;
				for (let pass = 0; pass < LANES; pass++) {
					const band = fwd ? block : block.concat([from, to]);
					const up = Math.min(...band.map((r) => r.y)) - LANE, down = Math.max(...band.map((r) => r.y + NH)) + LANE, want = (sy + ty) / 2;
					const more = [];
					for (const L of Math.abs(up - want) <= Math.abs(down - want) ? [up, down] : [down, up]) {
						if (L < PAD) continue;
						const r = lane(L, band, fwd), b = blockers(r.pts, rects);
						// A lane must not cross its own two cards either.
						if (!b.length && !blockers(r.pts, [from, to], -2).length) return done(r, false);
						b.forEach((x) => { if (block.indexOf(x) < 0 && more.indexOf(x) < 0) more.push(x); });
					}
					if (!more.length) break;
					block = block.concat(more);
				}
			}
			return done(plain, true);
		}

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
		// The live run: the frames by node name, the viewer's graph state, and the stream.
		let live = Object.create(null), liveHead = null, liveEnded = false, liveNote = "", source = null;
		const liveFrame = (name) => live[name] || null;
		// mark is the marker that starts a label, as the viewer does (labelMarker).
		const mark = (s) => { const r = Array.from(s || ""); return r.length >= 3 && r[0] === "[" && r[2] === "]" ? r.slice(0, 3).join("") : ""; };
		// words is a label without its marker, as the viewer does (labelWords).
		const words = (s) => { const m = mark(s); return m ? s.slice(m.length).replace(/^ /, "") : s || ""; };

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
			// A node with no position goes in a row, left to right, as the server fallback does.
			for (let i = pos.length; i < doc.nodes.length; i++) pos.push({ x: PAD + i * (NW + GAPX), y: PAD });
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
		// A change of the document, and undo or redo, first ends a drag: the
		// drag holds a node index and element that the change can make stale.
		function commit(fn, key) {
			if (drag) endDrag();
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
			if (drag) endDrag();
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
		// Text columns, as the server counts them (runeColumns): a combining
		// mark is 0, an East Asian wide rune 2, else 1.
		const WIDE = [[0x1100, 0x115f], [0x2e80, 0x303e], [0x3041, 0x33ff], [0x3400, 0x4dbf], [0x4e00, 0x9fff], [0xa000, 0xa4cf], [0xac00, 0xd7a3], [0xf900, 0xfaff], [0xfe30, 0xfe4f], [0xff00, 0xff60], [0xffe0, 0xffe6], [0x1f300, 0x1f64f], [0x1f900, 0x1f9ff], [0x20000, 0x3fffd]];
		const cols = (ch) => { const c = ch.codePointAt(0); return /[\p{Mn}\p{Me}]/u.test(ch) ? 0 : WIDE.some((w) => c >= w[0] && c <= w[1]) ? 2 : 1; };
		const take = (r, n) => { let k = 0, c = 0; while (k < r.length && c + cols(r[k]) <= n) { c += cols(r[k]); k++; } return k; };
		// cutEnd and cutMiddle keep a line inside the box, as the server does.
		// cutMiddle keeps the start and the end of a name, so names that differ
		// at the end stay apart.
		const cutEnd = (s, n) => { const r = Array.from(s || ""); return take(r, n) === r.length ? s || "" : r.slice(0, take(r, n - 1)).join("") + "…"; };
		function cutMiddle(s, n) {
			const r = Array.from(s || "");
			if (take(r, n) === r.length) return s || "";
			const budget = Math.max(n - 1, 0), head = take(r, Math.ceil(budget / 2));
			let tail = r.length, c = 0;
			while (tail > head && c + cols(r[tail - 1]) <= Math.floor(budget / 2)) { c += cols(r[tail - 1]); tail--; }
			// A combining mark whose base was cut goes too.
			while (tail < r.length && cols(r[tail]) === 0) tail++;
			return r.slice(0, head).join("") + "…" + r.slice(tail).join("");
		}
		const nodeProblems = (i) => check.problems.filter((p) => p.index === i);
		// pillWord is the edge label of a payload; none only sets the order.
		const pillWord = (payload) => (payload === "none" ? "order" : payload || "summary");
		// receives words what the incoming edges carry, as the viewer does (receivesText).
		function receives(name) {
			const count = { summary: 0, output: 0, order: 0 };
			let n = 0;
			doc.edges.forEach((e) => { if (e.to === name) { n++; count[pillWord(e.payload)] = (count[pillWord(e.payload)] || 0) + 1; } });
			if (!n) return "receives nothing, runs first";
			return "receives " + Object.keys(count).filter((k) => count[k]).map((k) => k + (count[k] > 1 ? " x" + count[k] : "")).join(", ");
		}
		// walk returns the names that the edges reach from start in one direction.
		function walk(start, from, to) {
			const seen = {}, queue = [start];
			seen[start] = true;
			while (queue.length) {
				const name = queue.pop();
				doc.edges.forEach((e) => { if (e[from] === name && !seen[e[to]]) { seen[e[to]] = true; queue.push(e[to]); } });
			}
			return seen;
		}
		// traced is the path of the selection: the ancestors and the descendants
		// of the selected node, or of both ends of the selected edge. It is null
		// when nothing is selected, and while a connect chooses its target.
		function traced() {
			if (!sel || link) return null;
			const e = sel.k === "edge" && doc.edges[sel.i], n = sel.k === "node" && doc.nodes[sel.i];
			const sets = [];
			(e ? [e.from, e.to] : n ? [n.name] : []).forEach((name) => { sets.push(walk(name, "from", "to"), walk(name, "to", "from")); });
			if (!sets.length) return null;
			return { node: (name) => sets.some((s) => s[name]), edge: (x) => sets.some((s) => s[x.from] && s[x.to]) };
		}
		const card = (i) => nodesG.children[i] || null;
		const place = (g, p) => g.setAttribute("transform", "translate(" + p.x + " " + p.y + ")");

		function render() {
			const active = document.activeElement;
			let refocus = active && active.classList && active.classList.contains("node") ? Number(active.dataset.i) : -1;
			// Keep the focus on a node only while it is still the selection.
			if (!(link || (sel && sel.k === "node" && sel.i === refocus))) refocus = -1;
			nodesG.textContent = "";
			freeCache = {};
			const path = traced();
			doc.nodes.forEach((n, i) => {
				const probs = nodeProblems(i).length;
				// A node with problems is "invalid": a dashed outline, not the failed look of a run.
				const g = svgEl("g", { class: "node" + (probs ? " invalid" : "") + (sel && sel.k === "node" && sel.i === i ? " selected" : "") + (link && link.target === i ? " target" : "") + (path && !path.node(n.name) ? " dim" : ""), tabindex: "0", role: "button", "data-i": i });
				g.appendChild(svgEl("title"));
				g.appendChild(svgEl("rect", { class: "ring", x: -5, y: -5, width: NW + 10, height: NH + 10, rx: 12 }));
				g.appendChild(svgEl("rect", { class: "box", width: NW, height: NH, rx: 8 }));
				g.appendChild(svgEl("path", { class: "band", d: BAND }));
				[["n-name", 18], ["n-mark", 18], ["n-sub", 44], ["n-status", 61], ["n-recv", 78], ["n-meta", 95]].forEach((t) => {
					g.appendChild(svgEl("text", t[0] === "n-mark" ? { class: t[0], x: NW - 12, y: t[1], "text-anchor": "end" } : t[0] === "n-name" ? { class: t[0], x: 14, y: t[1], "clip-path": "url(#name-clip)" } : { class: t[0], x: 14, y: t[1] }));
				});
				paintCard(g, n, probs);
				// The connect port and the "+" sit on the right edge, where edges leave.
				const port = svgEl("g", { class: "handle port" });
				port.appendChild(svgEl("circle", { cx: NW, cy: PORTY, r: 6 }));
				const add = svgEl("g", { class: "handle add" });
				add.appendChild(svgEl("circle", { cx: NW, cy: ADDY, r: 9 }));
				const plus = svgEl("text", { x: NW, y: ADDY + 5 });
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
			$("title").title = doc.name || "";
			document.title = (doc.name || "Draft") + " · Pipeline editor · Splice";
			renderProblems();
			renderHead();
			showLive();
			if (tab === "json") showJSON();
			if (refocus >= 0 && card(refocus)) {
				rendering = true;
				card(refocus).focus({ preventScroll: true });
				rendering = false;
			}
		}
		// paintCard writes the text and the status of a card: the draft facts,
		// and the live run state of the node with the same name. A problem
		// keeps its words on the status line; the run state is then in the
		// marker, the class, and the title.
		function paintCard(g, n, probs) {
			// The status line has the words only: the marker is in the header.
			const f = liveFrame(n.name), name = n.name || "(no name)", recv = receives(n.name), status = probs ? plural(probs, "problem") : f ? words(f.label) : label(n, 0);
			Array.from(g.classList).forEach((c) => { if (c.indexOf("s-") === 0) g.classList.remove(c); });
			g.classList.add("s-" + (f ? f.status : "static"));
			[[".n-name", cutMiddle(name, 21)], [".n-mark", f ? mark(f.label) : probs ? "[!]" : ""], [".n-sub", cutEnd(subtitle(n), 30)], [".n-status", cutEnd(status, 30)], [".n-recv", cutEnd(recv, 30)], [".n-meta", cutEnd(f && f.meta ? f.meta : n.model && n.model.model, 30)]].forEach((t) => {
				g.querySelector(t[0]).textContent = t[1];
			});
			const lines = [name, subtitle(n), label(n, probs)].concat(f ? ["run: " + f.label + (f.meta ? ", " + f.meta : "")] : [], [recv]);
			g.querySelector("title").textContent = lines.join("\n");
			g.setAttribute("aria-label", lines.join(", "));
		}
		let planeW = 0, planeH = 0;
		function sizePlane() {
			let w = 0, h = 0;
			pos.forEach((p) => { w = Math.max(w, p.x + NW); h = Math.max(h, p.y + NH); });
			// Routed edges, free lanes, and labels can go below the lowest node.
			if (routed && routed.nodes === nodeSet()) Object.keys(routed.paths).forEach((k) => { h = Math.max(h, routed.paths[k].bottom || 0); });
			Object.keys(freeCache).forEach((k) => { h = Math.max(h, freeCache[k].r.box[3]); w = Math.max(w, freeCache[k].r.box[2]); });
			h = Math.max(h, labelLow);
			planeW = Math.max(w + 48, (wrap.clientWidth - 2) / zoom);
			planeH = Math.max(h + 64, (wrap.clientHeight - 2) / zoom);
			svg.setAttribute("viewBox", "0 0 " + planeW + " " + planeH);
			svg.setAttribute("width", planeW * zoom);
			svg.setAttribute("height", planeH * zoom);
		}
		// freeCache keeps the free route of each edge by its index. render
		// clears it; a drag frame drops only the routes that can change (reroute).
		let freeCache = {}, labelLow = 0;
		function freeRoute(i, a, b) {
			const c = freeCache[i];
			if (c && c.a === a && c.b === b) return c.r;
			const r = routeFree(pos[a], pos[b], pos.filter((p, k) => k !== a && k !== b));
			freeCache[i] = { a: a, b: b, r: r };
			return r;
		}
		// reroute drops the free routes that a card move from old to now can
		// change: the edges of the card, and the routes whose box meets the card
		// at either place. A server path that the card moves into is checked in
		// route.
		function reroute(i, old, now) {
			const name = doc.nodes[i].name, meets = (b, p) => b[0] < p.x + NW + RM && b[2] > p.x - RM && b[1] < p.y + NH + RM && b[3] > p.y - RM;
			Object.keys(freeCache).forEach((k) => {
				const e = doc.edges[k];
				if (!e || e.from === name || e.to === name || meets(freeCache[k].r.box, old) || meets(freeCache[k].r.box, now)) delete freeCache[k];
			});
		}
		// drawEdges draws each edge with its payload pill. An edge takes its
		// server path while that is valid (route), else its free route
		// (routeFree). The pills go on top of every edge. A pill selects its
		// edge, as the edge does.
		function drawEdges() {
			edgesG.textContent = "";
			const path = traced(), pills = [], free = [];
			labelLow = 0;
			const items = doc.edges.map((e, i) => {
				const a = indexOfName(e.from), b = indexOfName(e.to);
				if (a < 0 || b < 0) return null;
				const word = pillWord(e.payload), sr = route(e, a, b), r = sr || freeRoute(i, a, b);
				const t = { e: e, i: i, d: r.d, x: r.x, y: r.y, word: word, w: 7 * word.length + 10 };
				if (!sr) free.push(t);
				return t;
			});
			// Free labels move apart as the server's do: 20 units down from a label
			// that they meet, and from a card. It ends: each step goes down, and
			// below every card and label there is room.
			const placed = [];
			free.sort((p, q) => p.x - q.x || p.y - q.y).forEach((t) => {
				while (placed.some((o) => Math.abs(o.x - t.x) < (o.w + t.w) / 2 + 4 && Math.abs(o.y - t.y) < 20) || pos.some((c) => t.x + t.w / 2 > c.x && t.x - t.w / 2 < c.x + NW && t.y + 8 > c.y && t.y - 8 < c.y + NH)) t.y += 20;
				placed.push(t);
				labelLow = Math.max(labelLow, t.y + 10);
			});
			items.forEach((t) => {
				if (!t) return;
				const e = t.e, i = t.i, d = t.d, dim = path && !path.edge(e) ? " dim" : "";
				// Live: an edge from a complete node has flowed. Data moves along an
				// edge into a running node, unless the edge only sets the order.
				const fa = liveFrame(e.from), fb = liveFrame(e.to);
				const flow = (fa && fa.status === "complete" ? " flowed" : "") + (fb && fb.status === "running" && e.payload !== "none" ? " flowing" : "");
				edgesG.appendChild(svgEl("path", { d: d, "marker-end": "url(#arrow)", class: "edge " + (e.payload || "summary") + flow + (sel && sel.k === "edge" && sel.i === i ? " sel" : "") + (check.problems.some((p) => p.edge === i) ? " badedge" : "") + dim }));
				edgesG.appendChild(svgEl("path", { d: d, class: "hit", "data-edge": i }));
				const pill = svgEl("g", { class: "pill" + dim, "data-edge": i, transform: "translate(" + t.x + " " + t.y + ")" });
				pill.appendChild(svgEl("rect", { x: -t.w / 2, y: -8, width: t.w, height: 16, rx: 8 }));
				pill.appendChild(svgEl("text", { y: 4, "text-anchor": "middle" })).textContent = t.word;
				pills.push(pill);
			});
			pills.forEach((p) => edgesG.appendChild(p));
		}
		function setZoom(z, quiet) {
			zoom = Math.min(Math.max(z, 0.3), 2);
			sizePlane();
			if (!quiet) say("Zoom " + Math.round(zoom * 100) + "%.");
		}
		// fit zooms out until the whole graph shows. The Fit tool and f go as
		// low as setZoom allows. On load, fit is quiet, runs only when the graph
		// does not fit, and stops at MINFIT so the cards stay readable: the
		// canvas then scrolls to the root column.
		const MINFIT = 0.85;
		function fit(auto) {
			let w = 0, h = 0;
			pos.forEach((p) => { w = Math.max(w, p.x + NW + PAD); h = Math.max(h, p.y + NH + PAD); });
			if (!w || (auto && w <= wrap.clientWidth && h <= wrap.clientHeight)) return;
			const z = Math.min(wrap.clientWidth / w, wrap.clientHeight / h, 1);
			setZoom(auto ? Math.max(z, MINFIT) : z, auto);
			wrap.scrollTo(0, 0);
			if (auto && z < MINFIT) scrollToRoot();
		}
		// scrollToRoot shows the left column, with its first node in view.
		function scrollToRoot() {
			const roots = doc.nodes.map((n, i) => i).filter((i) => !doc.edges.some((e) => e.to === doc.nodes[i].name));
			const i = roots.length ? roots.reduce((a, b) => (pos[b].x < pos[a].x || (pos[b].x === pos[a].x && pos[b].y < pos[a].y) ? b : a)) : 0;
			wrap.scrollTo(0, Math.max(0, (pos[i].y + NH / 2) * zoom - wrap.clientHeight / 2));
		}
		// routed is the last server layout: the node names, their positions, and
		// the routed path of each edge, which goes around the cards. An edge uses
		// its routed path while the node set is the same, both of its ends are
		// at their layout positions, and no card that left its layout position
		// sits on the path. Else it takes its free route.
		let routed = null;
		const nodeSet = () => doc.nodes.map((n) => n.name).join("\n");
		// layoutFor asks the server layout of the draft as it is now. The reply
		// keeps the node names of the request, so a later edit cannot pair with it.
		function layoutFor() {
			const names = doc.nodes.map((n) => n.name);
			return api("POST", "layout", { doc: doc }).then((d) => {
				const at = {}, paths = {};
				names.forEach((name, i) => { if (d.positions[i]) at[name] = d.positions[i]; });
				(d.paths || []).forEach((p) => { paths[p.from + "\n" + p.to] = p; });
				routed = { nodes: names.join("\n"), at: at, paths: paths };
				return d;
			});
		}
		function route(e, a, b) {
			const r = routed && routed.nodes === nodeSet() && routed.paths[e.from + "\n" + e.to];
			const still = (name, p) => { const q = routed.at[name]; return !!q && q.x === p.x && q.y === p.y; };
			if (!(r && still(e.from, pos[a]) && still(e.to, pos[b]))) return null;
			// A card that left its layout position may now sit on this path.
			const moved = pos.filter((p, k) => !still(doc.nodes[k].name, p));
			r.pts = r.pts || pathPoints(r.d);
			return moved.length && blockers(r.pts, moved).length ? null : r;
		}
		function relayout() {
			layoutFor()
				.then((d) => {
					const moved = commit(() => { pos = d.positions; });
					if (!moved) { drawEdges(); sizePlane(); }
					say(moved ? "Laid out as the viewer draws it. Press u to undo." : "The graph already has the viewer layout.");
				})
				.catch((err) => say(err.message, "error"));
		}

		// Panel folds. On a wide screen the Nodes panel and the inspector fold
		// to a slim rail, so the canvas gets the width. The choice persists in
		// localStorage when the browser allows it. Folds have no animation.
		const folds = { palette: "pal-folded", panel: "insp-folded" };
		function setFold(id, folded, save) {
			document.body.classList.toggle(folds[id], folded);
			const b = document.querySelector('[data-fold="' + id + '"]'), left = id === "palette";
			b.setAttribute("aria-expanded", String(!folded));
			b.setAttribute("aria-label", (folded ? "Show the " : "Hide the ") + (left ? "Nodes panel" : "inspector"));
			b.textContent = folded === left ? "›" : "‹";
			if (save) try { localStorage.setItem("splice.editor.fold." + id, folded ? "1" : ""); } catch (e) { /* the page works without storage */ }
			sizePlane();
		}
		function unfold(id) {
			if (document.body.classList.contains(folds[id])) setFold(id, false, false);
		}
		// showPanel opens the inspector: a sheet on a narrow screen, unfolded on a wide one.
		function showPanel() {
			document.body.classList.add("sheet");
			unfold("panel");
		}
		Object.keys(folds).forEach((id) => {
			let folded = false;
			try { folded = localStorage.getItem("splice.editor.fold." + id) === "1"; } catch (e) { /* no storage: unfolded */ }
			setFold(id, folded, false);
			document.querySelector('[data-fold="' + id + '"]').addEventListener("click", () => setFold(id, !document.body.classList.contains(folds[id]), true));
		});

		// Selection and panels.
		// focus selects, so an action that moves the focus decides the selection.
		function select(s, focus) {
			sel = s;
			if (link && !(s && s.k === "node")) endLink();
			document.body.classList.toggle("sheet", !!s);
			if (s) unfold("panel");
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
			if (!check.problems.length && !warn.children.length) list.appendChild(el("li", "none", "[+] None."));
		}
		function goTo(p) {
			if (p.index !== undefined && p.index < doc.nodes.length) select({ k: "node", i: p.index }, false);
			else if (p.edge !== undefined && p.edge < doc.edges.length) select({ k: "edge", i: p.edge }, false);
			else { select(null, false); showPanel(); }
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
			unfold("palette");
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
			// The new node goes in the next column: right of the anchor, else right
			// of the rightmost node. It moves down until it covers no node.
			const anchor = from >= 0 ? pos[from] : sel && sel.k === "node" ? pos[sel.i] : pos.reduce((right, p) => (p.x > right.x ? p : right), { x: PAD - NW - GAPX, y: PAD });
			const at = { x: anchor.x + NW + GAPX, y: anchor.y };
			while (pos.some((p) => Math.abs(p.x - at.x) < NW + 8 && Math.abs(p.y - at.y) < NH + 8)) at.y += NH + GAPY;
			const node = { name: name, type: type };
			if (facts(type).prompt) node.prompt = "";
			commit(() => {
				// Inside the change, so undo takes the version back too.
				if (type === "agent" && !doc.splice_min_version) doc.splice_min_version = boot.agent_min;
				doc.nodes.push(node);
				pos.push(at);
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
		// numberOrNone returns a whole number for a count field: a fraction is
		// truncated, and a value beyond the safe integer range is clamped.
		function numberOrNone(v) {
			const t = String(v).trim();
			if (t === "") return undefined;
			const x = Number(t), whole = Math.trunc(Math.min(Math.max(x, -Number.MAX_SAFE_INTEGER), Number.MAX_SAFE_INTEGER));
			// A positive value below 1 becomes 1, since a turn limit of 0 means the default.
			// A negative value above -1 becomes -1, so the server reports it.
			return x > 0 && whole < 1 ? 1 : x < 0 && whole > -1 ? -1 : whole;
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
						// A name that another node has would make edges ambiguous, so it is refused.
						if (t.value !== old && indexOfName(t.value) >= 0) {
							say("Another node is named " + (t.value || "(no name)") + ". The name stays " + (old || "(no name)") + " until it is unique.", "error");
							break;
						}
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
		// A refused name is not kept in the box: leaving the field restores the document's name.
		$("n-name").addEventListener("change", () => {
			const i = inspected();
			if (i < 0 || $("n-name").value === doc.nodes[i].name) return;
			setVal("n-name", doc.nodes[i].name);
			say("The name was kept as " + (doc.nodes[i].name || "(no name)") + ".", "error");
		});
		// A number box shows the stored value once the user leaves it.
		["n-in", "n-out", "n-turns", "t-in", "t-out"].forEach((id) => $(id).addEventListener("change", () => {
			const v = numberOrNone($(id).value);
			if (v !== undefined) setVal(id, v);
		}));
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
			drag = { i: i, sx: e.clientX, sy: e.clientY, ox: pos[i].x, oy: pos[i].y, moved: false };
			live.setPointerCapture(e.pointerId);
		});
		// endDrag puts a dragged node back. A move with no button ends a drag
		// whose pointerup was missed, so a hover never moves a node.
		function endDrag() {
			const el = drag && card(drag.i);
			if (el) {
				pos[drag.i] = { x: drag.ox, y: drag.oy };
				place(el, pos[drag.i]);
				freeCache = {};
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
				rubber.setAttribute("d", "M" + (a.x + NW) + " " + (a.y + PORTY) + " L" + p.x + " " + p.y);
				return;
			}
			const dx = (e.clientX - drag.sx) / zoom, dy = (e.clientY - drag.sy) / zoom;
			if (!drag.moved && Math.abs(dx) + Math.abs(dy) < 4) return;
			drag.moved = true;
			drag.nx = Math.max(0, drag.ox + dx);
			drag.ny = Math.max(0, drag.oy + dy);
			if (card(drag.i)) card(drag.i).classList.add("dragging");
			if (!raf) raf = requestAnimationFrame(frame);
		});
		// frame moves the dragged node by its transform, once per animation frame.
		function frame() {
			raf = 0;
			if (!drag || drag.link !== undefined) return;
			const old = pos[drag.i];
			pos[drag.i] = { x: drag.nx, y: drag.ny };
			if (card(drag.i)) place(card(drag.i), pos[drag.i]);
			reroute(drag.i, old, pos[drag.i]);
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
			// A drop at the start position changes nothing, so commit draws
			// nothing: put the card and its edges back here.
			if (!commit(() => { pos[d.i] = final; })) {
				if (card(d.i)) place(card(d.i), pos[d.i]);
				freeCache = {};
				drawEdges();
			}
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
				case "s": select(null, false); showTab("form"); showPanel(); $("t-name").focus(); break;
				case "j": showPanel(); showTab("json"); $("json").focus(); break;
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
					else if (sel) { showPanel(); showTab("form"); (sel.k === "node" ? $("n-name") : $("deps").querySelector("li.hl select") || $("dep-add")).focus(); }
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
		on("settings", () => { select(null, false); showTab("form"); showPanel(); $("t-name").focus(); });
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
		on("z-fit", () => fit());
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

		// Live run. The editor shows the run of the pipeline viewer on the nodes
		// with the same name. It loads the viewer's graph state and follows its
		// stream, with the rules of pipeline.js. A server without the viewer,
		// such as `splice pipeline edit`, says so in the boot data.
		function liveGraph() {
			return fetch("../pipeline/graph").then((r) => {
				if (!r.ok) throw new Error(String(r.status));
				return r.json();
			}).then((d) => {
				if (liveEnded) return;
				liveHead = d;
				live = Object.create(null);
				// The static graph is the active topology, not a run: nothing to paint.
				if (d.run_state !== "static") (d.nodes || []).forEach((f) => { live[f.name] = f; });
				paintLive();
			});
		}
		function paintLive() {
			doc.nodes.forEach((n, i) => { if (card(i)) paintCard(card(i), n, nodeProblems(i).length); });
			drawEdges();
			showLive();
		}
		// showLive is the run line in the top bar: the viewer's run text, and a
		// note when the draft is not the topology that runs.
		function showLive() {
			const line = $("live"), d = liveHead;
			const on = !!d && d.run_state !== "static";
			line.hidden = !on && !liveNote;
			if (!on) { line.textContent = liveNote; line.dataset.state = "stopped"; return; }
			// The topology name shows only when it is not the draft name.
			const about = [d.topology === doc.name ? "" : d.topology, d.facts].filter(Boolean).join(", ");
			line.dataset.state = d.run_state;
			line.textContent = "Run" + (about ? " (" + about + ")" : "") + ": " + d.run + (d.topology !== doc.name ? " The draft differs from the run. Live state shows on the nodes with the same name." : "");
		}
		function endLive(note) {
			const ran = liveHead && liveHead.run_state !== "static";
			liveEnded = true;
			if (source) source.close();
			live = Object.create(null);
			liveHead = null;
			liveNote = ran ? note : "";
			paintLive();
		}
		// boot.viewer is false on a server without the viewer: no request, no stream.
		(boot.viewer ? liveGraph() : Promise.reject(new Error("no viewer"))).then(() => {
			source = new EventSource("../../events");
			const listen = (name, fn) => source.addEventListener(name, (e) => {
				if (liveEnded) return;
				try { fn(JSON.parse(e.data)); } catch (err) { /* a malformed event changes nothing */ }
			});
			listen("pipeline-node", (f) => {
				if (!liveHead || f.run_seq !== liveHead.run_seq || liveHead.run_state === "static") return;
				live[f.name] = f;
				paintLive();
			});
			// A graph event is a new run, a new layout, or a new run line: load it all.
			listen("pipeline-graph", () => { liveGraph().catch(() => {}); });
			source.addEventListener("session-changed", () => endLive("The terminal session changed, so the live run state stopped."));
			// The browser retries a lost stream by itself. Three failed tries in a
			// row, or a lost stream after the run ended, mean that the server stopped.
			let failures = 0;
			source.addEventListener("open", () => { failures = 0; if (!liveEnded) liveGraph().catch(() => {}); });
			source.addEventListener("error", () => {
				if (liveEnded) return;
				const state = liveHead ? liveHead.run_state : "";
				if (++failures >= 3 || source.readyState === EventSource.CLOSED || state === "completed" || state === "failed" || state === "cancelled") endLive("Splice stopped serving the live run state.");
			});
		}).catch(() => { /* no viewer on this server: no live state */ });

		render();
		fillPanel();
		fit(true);
		// A draft at its auto-layout positions shows the routed edges at once.
		layoutFor().then(() => { drawEdges(); sizePlane(); }).catch(() => {});
		if (!doc.nodes.length) $("pal-search").focus();
	}
})();
