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

	// cut keeps a line inside the box, as the server does (layoutLineRunes).
	function cut(s) {
		var r = Array.from(s || "");
		return r.length > 26 ? r.slice(0, 25).join("") + "…" : s || "";
	}
	// applyFrame mutates one node box and the edges that leave it.
	function applyFrame(el, f) {
		el.setAttribute("data-iteration", String(f.iteration));
		el.setAttribute("class", "node s-" + f.status + (f.name === selected ? " selected" : ""));
		el.setAttribute("aria-label", f.name + ", " + f.label);
		text(el, ".n-status", cut(f.label));
		text(el, ".n-meta", cut(f.meta));
		var title = el.querySelector("title");
		if (title) {
			var lines = title.textContent.split("\n");
			lines[2] = f.label;
			title.textContent = lines.join("\n");
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

	// scrollToRoot opens a graph wider than its frame with the root node in
	// view, instead of the left edge.
	function scrollToRoot() {
		var root = nodes()[0];
		if (!graph || !root || graph.scrollWidth <= graph.clientWidth) return;
		var box = root.getBoundingClientRect();
		var frame = graph.getBoundingClientRect();
		graph.scrollLeft += box.left - frame.left - (graph.clientWidth - box.width) / 2;
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

	document.addEventListener("keydown", function (e) {
		if (e.metaKey || e.ctrlKey || e.altKey) return;
		var onRange = e.target === scrub;
		switch (e.key) {
			case "j":
			case "ArrowDown":
				if (onRange && e.key !== "j") return;
				move(1);
				break;
			case "k":
			case "ArrowUp":
				if (onRange && e.key !== "k") return;
				move(-1);
				break;
			case "Enter":
				if (e.target.tagName === "BUTTON") return;
				if (!selected && nodes().length) select(nodes()[0].getAttribute("data-name"), true);
				if (selected) openDrawer(selected);
				break;
			case "Escape":
				closeDrawer();
				break;
			case "[":
				stepBy(-1);
				break;
			case "]":
				stepBy(1);
				break;
			default:
				return;
		}
		e.preventDefault();
	});
	if (graph) {
		graph.addEventListener("click", function (e) {
			var el = e.target.closest ? e.target.closest("g.node") : null;
			if (!el) return;
			select(el.getAttribute("data-name"), true);
			openDrawer(el.getAttribute("data-name"));
		});
		graph.addEventListener("focusin", function (e) {
			var el = e.target.closest ? e.target.closest("g.node") : null;
			if (el && el.getAttribute("data-name") !== selected) select(el.getAttribute("data-name"), false);
		});
	}
	$("drawer-close").addEventListener("click", closeDrawer);
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
