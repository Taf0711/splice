// Pipeline viewer. The server renders the graph; this script only flips
// classes and text on the nodes that an event names. Every URL is relative to
// the page, so the page works under any path prefix.
(function () {
	"use strict";

	var $ = function (id) { return document.getElementById(id); };
	var graph = $("graph");
	var drawer = $("drawer");
	var fieldsEl = $("drawer-fields");
	var drawerTitle = $("drawer-title");
	var drawerError = $("drawer-error");
	var drawerBudget = $("drawer-budget");
	var notice = $("notice");
	var runEl = $("run");
	var timeline = $("timeline");
	var scrub = $("scrub");
	var scrubLabel = $("scrub-label");

	var selected = null; // the selected node name
	var drawerFor = null; // the node the open drawer shows
	var drawerSeq = 0; // drops a drawer reply that a newer request replaced
	var drawerTimer = 0;
	var steps = [];
	var saved = null; // the node state before a replay, restored at the end
	var ended = false;
	var currentRun = Number(document.body.getAttribute("data-run") || 0);
	var gone = ""; // "session" or "stopped" once the stream ends for good

	function nodes() {
		return graph ? Array.prototype.slice.call(graph.querySelectorAll("g.node")) : [];
	}
	function nodeEl(name) {
		var all = nodes();
		for (var i = 0; i < all.length; i++) {
			if (all[i].getAttribute("data-name") === name) return all[i];
		}
		return null;
	}
	function edgesFrom(name) {
		if (!graph) return [];
		return Array.prototype.filter.call(graph.querySelectorAll("path.edge"), function (e) {
			return e.getAttribute("data-from") === name;
		});
	}
	function text(el, sel, value) {
		var t = el.querySelector(sel);
		if (t) t.textContent = value;
	}

	// Text columns, as the server counts them (runeColumns): a combining mark
	// is 0, an East Asian wide rune 2, else 1.
	var WIDE = [[0x1100, 0x115f], [0x2e80, 0x303e], [0x3041, 0x33ff], [0x3400, 0x4dbf], [0x4e00, 0x9fff], [0xa000, 0xa4cf], [0xac00, 0xd7a3], [0xf900, 0xfaff], [0xfe30, 0xfe4f], [0xff00, 0xff60], [0xffe0, 0xffe6], [0x1f300, 0x1f64f], [0x1f900, 0x1f9ff], [0x20000, 0x3fffd]];
	function cols(ch) {
		var c = ch.codePointAt(0);
		return /[\p{Mn}\p{Me}]/u.test(ch) ? 0 : WIDE.some(function (w) { return c >= w[0] && c <= w[1]; }) ? 2 : 1;
	}
	function take(r, n) {
		var k = 0, c = 0;
		while (k < r.length && c + cols(r[k]) <= n) { c += cols(r[k]); k++; }
		return k;
	}
	// cutEnd keeps a line inside the box, as the server does (cutEnd).
	function cutEnd(s, n) {
		var r = Array.from(s || "");
		return take(r, n) === r.length ? s || "" : r.slice(0, take(r, n - 1)).join("") + "…";
	}
	// mark is the marker that starts a label, as the server does (labelMarker).
	function mark(label) {
		var r = Array.from(label || "");
		return r.length >= 3 && r[0] === "[" && r[2] === "]" ? r.slice(0, 3).join("") : "";
	}
	// words is a label without its marker, as the server does (labelWords).
	function words(label) {
		var m = mark(label);
		return m ? label.slice(m.length).replace(/^ /, "") : label || "";
	}
	// applyFrame mutates one node box and the edges that leave it.
	function applyFrame(el, f) {
		el.setAttribute("data-iteration", String(f.iteration));
		el.setAttribute("class", "node s-" + f.status + (f.name === selected ? " selected" : "") + (el.classList.contains("dim") ? " dim" : ""));
		text(el, ".n-mark", mark(f.label));
		text(el, ".n-status", cutEnd(words(f.label), 30));
		text(el, ".n-meta", cutEnd(f.meta, 30));
		var title = el.querySelector("title");
		if (title) {
			// The title lines: the name, the type, the status, and the receives line.
			var lines = title.textContent.split("\n");
			lines[2] = f.label;
			title.textContent = lines.join("\n");
			el.setAttribute("aria-label", f.name + ", " + f.label + ", " + lines[3]);
		}
		edgesFrom(f.name).forEach(function (e) {
			e.classList.toggle("flowed", f.status === "complete");
		});
	}

	// pulse marks the edges a summary moves along when a node completes.
	function pulse(name) {
		edgesFrom(name).forEach(function (e) {
			e.classList.remove("pulse");
			void e.getBoundingClientRect(); // restart the animation
			e.classList.add("pulse");
			var done = function () { e.classList.remove("pulse"); };
			e.addEventListener("animationend", done, { once: true });
			setTimeout(done, 2000); // reduced motion has no animationend
		});
	}

	// onNode applies one node frame. The stream is in order and keeps only the
	// latest frame per node, so the only frame to drop is one of another run.
	function onNode(f) {
		if (saved || f.run_seq !== currentRun) return;
		var el = nodeEl(f.name);
		if (!el) return;
		var was = el.getAttribute("class") || "";
		applyFrame(el, f);
		if (f.status === "complete" && was.indexOf("s-complete") < 0) pulse(f.name);
		if (drawerFor === f.name) refreshDrawer();
	}

	function onGraph(d) {
		var svg = graph && graph.querySelector("svg");
		if (d.run_seq !== currentRun || d.resync || (graph && (!svg || svg.getAttribute("data-layout") !== d.layout))) {
			reloadGraph();
			return;
		}
		applyHeader(d);
		if (drawerFor) refreshDrawer();
	}

	function applyHeader(d) {
		$("topology").textContent = d.topology || "";
		$("topology").title = d.topology || "";
		$("facts").textContent = d.facts || "";
		runEl.textContent = d.run;
		runEl.setAttribute("data-state", d.run_state);
		var notes = $("notes");
		notes.textContent = "";
		(d.notes || []).forEach(function (n) {
			var li = document.createElement("li");
			li.textContent = n;
			notes.appendChild(li);
		});
	}

	// reloadGraph loads the whole graph state: on every stream connect, so a
	// lost or late stream cannot leave the page stale, and for a new run or a
	// new layout.
	function reloadGraph() {
		fetch("graph").then(function (r) {
			if (!r.ok) throw new Error(r.status);
			return r.json();
		}).then(function (d) {
			if (!d.svg !== !graph) {
				location.reload(); // the page changed between a graph and a problem text
				return;
			}
			if (graph) graph.innerHTML = d.svg;
			applyZoom();
			scrollToRoot();
			currentRun = d.run_seq;
			saved = null;
			applyHeader(d);
			if (selected && !nodeEl(selected)) selected = null;
			markSelected(false);
			if (d.has_timeline) loadTimeline();
			else setSteps([]);
			if (drawerFor) refreshDrawer();
		}).catch(function () {});
	}
	function loadTimeline() {
		fetch("timeline").then(function (r) { return r.json(); }).then(function (d) { setSteps(d.steps); }).catch(function () {});
	}

	// scrollToRoot opens the graph with the root column in view. The root is in
	// the left column, and a column is centered vertically, so a graph taller
	// than its frame also scrolls down to the root node.
	function scrollToRoot() {
		var root = nodes()[0];
		if (!graph || !root) return;
		graph.scrollLeft = 0;
		if (graph.scrollHeight <= graph.clientHeight) return;
		var box = root.getBoundingClientRect();
		var frame = graph.getBoundingClientRect();
		graph.scrollTop += box.top - frame.top - (graph.clientHeight - box.height) / 2;
	}

	// Zoom scales the drawn size of the svg; its viewBox keeps the layout.
	// The page opens at 100 percent with the root in view. Zoom has no animation.
	var zoom = 1;
	function graphSize() {
		var svg = graph && graph.querySelector("svg");
		var box = svg ? svg.getAttribute("viewBox").split(" ").map(Number) : [];
		return svg ? { svg: svg, w: box[2], h: box[3] } : null;
	}
	function applyZoom() {
		var g = graphSize();
		if (!g) return;
		g.svg.setAttribute("width", g.w * zoom);
		g.svg.setAttribute("height", g.h * zoom);
	}
	function setZoom(z) {
		zoom = Math.min(Math.max(z, 0.3), 2);
		applyZoom();
	}
	function fitGraph() {
		var g = graphSize();
		if (!g) return;
		var room = parseFloat(getComputedStyle(graph).maxHeight) - 16;
		setZoom(Math.min((graph.clientWidth - 16) / g.w, room > 0 ? room / g.h : 1, 1));
		graph.scrollTo(0, 0);
	}
	function resetZoom() {
		setZoom(1);
		scrollToRoot();
	}

	// Selection uses a roving tabindex: Tab reaches the graph once, j and k move.
	// Nothing is selected until the user selects a node; the first node then
	// holds the tab stop.
	function markSelected(focus) {
		var all = nodes();
		var stop = selected && nodeEl(selected) ? selected : (all.length ? all[0].getAttribute("data-name") : null);
		all.forEach(function (el) {
			var name = el.getAttribute("data-name");
			var on = name === selected;
			el.classList.toggle("selected", on);
			el.setAttribute("tabindex", name === stop ? "0" : "-1");
			if (on && focus) {
				el.focus();
				el.scrollIntoView({ block: "nearest", inline: "nearest" });
			}
		});
		trace();
	}
	// reach returns the names that the edges reach from start, in one
	// direction: from "data-from" to "data-to" is downstream.
	function reach(links, start, from, to) {
		var seen = {}, queue = [start];
		seen[start] = true;
		while (queue.length) {
			var name = queue.pop();
			links.forEach(function (e) {
				var next = e.getAttribute(to);
				if (e.getAttribute(from) === name && !seen[next]) {
					seen[next] = true;
					queue.push(next);
				}
			});
		}
		return seen;
	}
	// trace keeps the path of the selected node at full strength: its
	// ancestors, its descendants, and the edges between them. The rest dims.
	function trace() {
		if (!graph) return;
		var on = !!(selected && nodeEl(selected));
		var links = Array.prototype.slice.call(graph.querySelectorAll("path.edge"));
		var up = on ? reach(links, selected, "data-to", "data-from") : {};
		var down = on ? reach(links, selected, "data-from", "data-to") : {};
		nodes().forEach(function (el) {
			var n = el.getAttribute("data-name");
			el.classList.toggle("dim", on && !up[n] && !down[n]);
		});
		Array.prototype.forEach.call(graph.querySelectorAll("[data-from]"), function (e) {
			var a = e.getAttribute("data-from"), b = e.getAttribute("data-to");
			e.classList.toggle("dim", on && !(up[a] && up[b]) && !(down[a] && down[b]));
		});
	}
	function clearSelection() {
		selected = null;
		markSelected(false);
	}
	function select(name, focus) {
		selected = name;
		markSelected(focus);
		if (drawerFor && drawerFor !== name) openDrawer(name);
	}
	function move(delta) {
		var all = nodes();
		if (!all.length) return;
		var i = all.map(function (el) { return el.getAttribute("data-name"); }).indexOf(selected);
		i = Math.min(Math.max(i + delta, 0), all.length - 1);
		select(all[i].getAttribute("data-name"), true);
	}

	function openDrawer(name) {
		drawerFor = name;
		if (drawer.hidden) {
			// Keep the graph where it is: the canvas narrows for the drawer, and a
			// centered graph would jump.
			var svg = graph && graph.querySelector("svg");
			var left = svg ? svg.getBoundingClientRect().left - graph.getBoundingClientRect().left : 0;
			drawer.hidden = false;
			drawer.parentNode.classList.add("drawer-open");
			document.body.classList.add("has-drawer");
			if (svg && wide.matches) {
				var room = graph.clientWidth - svg.getBoundingClientRect().width - 16;
				svg.style.marginLeft = Math.max(0, Math.min(left - 8, room)) + "px";
			}
		}
		loadDrawer();
	}
	var wide = window.matchMedia("(min-width: 900px)");
	function refreshDrawer() {
		clearTimeout(drawerTimer);
		drawerTimer = setTimeout(loadDrawer, 250);
	}
	function loadDrawer() {
		if (!drawerFor) return;
		var seq = ++drawerSeq;
		var url = "node?name=" + encodeURIComponent(drawerFor);
		drawerTitle.textContent = drawerFor;
		if (replayStep >= 0) {
			url += "&step=" + replayStep;
			drawerTitle.textContent = drawerFor + " · step " + replayStep;
		}
		fetch(url).then(function (r) {
			if (!r.ok) throw new Error(r.status);
			return r.json();
		}).then(function (d) {
			if (seq !== drawerSeq) return;
			drawerError.hidden = true;
			fieldsEl.textContent = "";
			d.fields.forEach(function (f) {
				var dt = document.createElement("dt");
				var dd = document.createElement("dd");
				dt.textContent = f.key;
				dd.textContent = f.value;
				fieldsEl.appendChild(dt);
				fieldsEl.appendChild(dd);
			});
			renderBudget(d.budget);
		}).catch(function () {
			if (seq !== drawerSeq) return;
			drawerError.hidden = false;
			drawerError.textContent = unavailable();
		});
	}
	// unavailable says why the details did not load. The cause decides the words.
	function unavailable() {
		if (gone === "session") return "The terminal session changed; details are unavailable. Run /web in Splice for a new link.";
		var state = runEl.getAttribute("data-state");
		var runEnded = state === "completed" || state === "failed" || state === "cancelled";
		if (runEnded) return "The run ended and the server stopped; details are unavailable.";
		if (gone === "stopped") return "The server stopped; details are unavailable.";
		return "The details did not load. Try again.";
	}
	// renderBudget draws the latest pass's token use against its budget: a
	// native meter for each direction, and the share in words.
	function renderBudget(b) {
		drawerBudget.textContent = "";
		drawerBudget.hidden = !b;
		if (!b) return;
		var head = document.createElement("p");
		head.className = "budget-head";
		head.textContent = b.configured ? "Budget use (configured budget)" : "Budget use (this pass)";
		drawerBudget.appendChild(head);
		[["Input", b.input_used, b.input_max], ["Output", b.output_used, b.output_max]].forEach(function (row) {
			if (!(row[2] > 0)) return;
			var share = Math.round(row[1] / row[2] * 100);
			var line = document.createElement("div");
			line.className = "budget-row" + (row[1] > row[2] ? " over" : "");
			var label = document.createElement("span");
			label.textContent = row[0];
			var meter = document.createElement("meter");
			meter.min = 0;
			meter.max = row[2];
			meter.low = row[2] * 0.8;
			meter.high = row[2];
			meter.optimum = 0;
			meter.value = Math.min(row[1], row[2]);
			meter.setAttribute("aria-label", row[0] + " tokens against the budget");
			var words = document.createElement("span");
			words.textContent = share + "%" + (row[1] > row[2] ? ", over the budget" : "");
			line.appendChild(label);
			line.appendChild(meter);
			line.appendChild(words);
			drawerBudget.appendChild(line);
		});
	}
	function closeDrawer() {
		if (!drawerFor) return;
		drawerFor = null;
		drawer.hidden = true;
		drawer.parentNode.classList.remove("drawer-open");
		document.body.classList.remove("has-drawer");
		var svg = graph && graph.querySelector("svg");
		if (svg) svg.style.marginLeft = "";
		markSelected(true);
	}

	// The timeline replays the run. Step 0 is the plan before any record, step
	// k is the state after record k, and the last position is the end of the
	// run, which restores the final state. While a step shows, the header and
	// the drawer describe that step, not the end of the run.
	var replayStep = -1; // the step that shows, or -1 at the end of the run
	function records() { return steps.length - 1; }
	function setSteps(list) {
		steps = list || [];
		if (saved) restore();
		timeline.hidden = steps.length === 0;
		if (!steps.length) return;
		scrub.min = "0";
		scrub.max = String(steps.length);
		scrub.value = String(steps.length);
		scrubLabel.textContent = endLabel();
	}
	function endLabel() {
		return "End of the run, after " + records() + " stage records. Move the slider to replay it from the plan.";
	}
	function save() {
		saved = {
			run: runEl.textContent,
			runState: runEl.getAttribute("data-state"),
			nodes: nodes().map(function (el) {
				return {
					el: el,
					cls: el.getAttribute("class"),
					it: el.getAttribute("data-iteration"),
					aria: el.getAttribute("aria-label"),
					mark: el.querySelector(".n-mark").textContent,
					status: el.querySelector(".n-status").textContent,
					meta: el.querySelector(".n-meta").textContent
				};
			})
		};
	}
	function restore() {
		saved.nodes.forEach(function (s) {
			s.el.setAttribute("class", s.cls);
			s.el.setAttribute("data-iteration", s.it);
			s.el.setAttribute("aria-label", s.aria);
			text(s.el, ".n-mark", s.mark);
			text(s.el, ".n-status", s.status);
			text(s.el, ".n-meta", s.meta);
			edgesFrom(s.el.getAttribute("data-name")).forEach(function (e) {
				e.classList.toggle("flowed", s.cls.indexOf("s-complete") >= 0);
			});
		});
		runEl.textContent = saved.run;
		runEl.setAttribute("data-state", saved.runState);
		saved = null;
		replayStep = -1;
		markSelected(false);
	}
	function showStep(k) {
		if (k >= steps.length) {
			if (saved) restore();
			scrubLabel.textContent = endLabel();
		} else {
			if (!saved) save();
			replayStep = k;
			steps[k].nodes.forEach(function (f) {
				var el = nodeEl(f.name);
				if (el) applyFrame(el, f);
			});
			markSelected(false);
			runEl.textContent = "Replaying step " + k + " of " + records() + ".";
			runEl.setAttribute("data-state", "replaying");
			scrubLabel.textContent = "Step " + k + " of " + records() + ": " + steps[k].caption;
		}
		if (drawerFor) loadDrawer();
	}
	function stepBy(delta) {
		if (!steps.length) return;
		var k = Math.min(Math.max(Number(scrub.value) + delta, 0), steps.length);
		scrub.value = String(k);
		showStep(k);
	}
	scrub.addEventListener("input", function () { showStep(Number(scrub.value)); });

	function showNotice(message) {
		notice.hidden = false;
		notice.textContent = message;
	}

	// The arrow keys select only while the graph has the focus; elsewhere they
	// scroll the page, so a scroll never selects a node.
	function inGraph(target) {
		return !!(graph && target && target !== scrub && graph.contains(target));
	}
	// A page that the browser restores from its back-forward cache keeps the
	// selection of the earlier visit. Nothing is selected until the user
	// selects, so drop it.
	window.addEventListener("pageshow", function (e) {
		if (!e.persisted) return;
		selected = null;
		if (drawerFor) closeDrawer();
		clearSelection();
	});
	document.addEventListener("keydown", function (e) {
		if (e.metaKey || e.ctrlKey || e.altKey) return;
		switch (e.key) {
			case "j":
			case "ArrowDown":
				if (e.key !== "j" && !inGraph(e.target)) return;
				move(1);
				break;
			case "k":
			case "ArrowUp":
				if (e.key !== "k" && !inGraph(e.target)) return;
				move(-1);
				break;
			case "Enter":
				if (e.target.tagName === "BUTTON") return;
				if (!selected && nodes().length) select(nodes()[0].getAttribute("data-name"), true);
				if (selected) openDrawer(selected);
				break;
			case "Escape":
				if (drawerFor) closeDrawer();
				else if (selected) clearSelection();
				else return;
				break;
			case "[":
				stepBy(-1);
				break;
			case "]":
				stepBy(1);
				break;
			case "+":
			case "=":
				setZoom(zoom * 1.2);
				break;
			case "-":
				setZoom(zoom / 1.2);
				break;
			case "0":
				resetZoom();
				break;
			case "f":
				fitGraph();
				break;
			default:
				return;
		}
		e.preventDefault();
	});
	if (graph) {
		graph.addEventListener("click", function (e) {
			var el = e.target.closest ? e.target.closest("g.node") : null;
			if (!el) {
				// A click on the empty canvas clears the selection and its path.
				selected = null;
				if (drawerFor) closeDrawer();
				clearSelection();
				return;
			}
			select(el.getAttribute("data-name"), true);
			openDrawer(el.getAttribute("data-name"));
		});
		// Keyboard focus on a node selects it. A focus that the user did not
		// move with the keyboard (a pointer press, a restored page) does not:
		// a click selects through its own handler.
		graph.addEventListener("focusin", function (e) {
			var el = e.target.closest ? e.target.closest("g.node") : null;
			if (el && el.matches(":focus-visible") && el.getAttribute("data-name") !== selected) select(el.getAttribute("data-name"), false);
		});
	}
	$("drawer-close").addEventListener("click", closeDrawer);
	if (graph) {
		$("z-in").addEventListener("click", function () { setZoom(zoom * 1.2); });
		$("z-out").addEventListener("click", function () { setZoom(zoom / 1.2); });
		$("z-fit").addEventListener("click", fitGraph);
		$("z-reset").addEventListener("click", resetZoom);
	}
	markSelected(false);
	scrollToRoot();


	var source = new EventSource("../../events");
	function listen(name, fn) {
		source.addEventListener(name, function (e) {
			if (ended) return;
			try { fn(JSON.parse(e.data)); } catch (err) { /* a malformed event changes nothing */ }
		});
	}
	listen("pipeline-node", onNode);
	listen("pipeline-graph", onGraph);
	listen("pipeline-timeline", function (d) { setSteps(d.steps); });
	source.addEventListener("session-changed", function () {
		ended = true;
		gone = "session";
		source.close();
		document.body.classList.add("ended");
		showNotice("The terminal session changed, so this page no longer updates. Run /web in Splice for a new link.");
	});
	// The browser retries a lost stream by itself. Three failed tries in a row,
	// or a lost stream after the run ended, mean that the server stopped.
	var failures = 0;
	source.addEventListener("open", function () {
		failures = 0;
		if (ended) return;
		notice.hidden = true;
		reloadGraph();
	});
	source.addEventListener("error", function () {
		if (ended) return;
		failures++;
		var state = runEl.getAttribute("data-state");
		if (source.readyState === EventSource.CLOSED || failures >= 3 || state === "completed" || state === "failed" || state === "cancelled") {
			ended = true;
			gone = "stopped";
			source.close();
			document.body.classList.add("ended");
			// The run line must not say that a run still runs.
			var shown = saved ? saved.runState : state;
			if (shown === "running") {
				if (saved) {
					saved.run = "Running when Splice stopped.";
					saved.runState = "stopped";
				} else {
					runEl.textContent = "Running when Splice stopped.";
					runEl.setAttribute("data-state", "stopped");
				}
			}
			showNotice("Splice stopped serving this page. The page shows the last state it received. Run /web in Splice for a new link.");
			return;
		}
		showNotice("The connection to Splice was lost. The page tries to connect again.");
	});
})();
