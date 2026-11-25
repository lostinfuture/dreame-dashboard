(function () {
  var cards = Array.prototype.slice.call(document.querySelectorAll(".chart-card"));
  if (!cards.length) return;

  var mounted = [];

  function num(v) {
    if (v == null || isNaN(v)) return "—";
    var av = Math.abs(v);
    if (av < 0.0005) return "0";
    if (av < 1) return String(Number(v.toFixed(3)));
    var n = Math.round(v * 10) / 10;
    return Math.abs(n - Math.round(n)) < 0.05 ? String(Math.round(n)) : n.toFixed(1);
  }

  function niceTop(v) {
    if (v <= 0) return 1;
    var step = 200;
    if (v <= 0.2) step = 0.04;
    else if (v <= 1) step = 0.2;
    else if (v <= 20) step = 4;
    else if (v <= 100) step = 8;
    else if (v <= 400) step = 40;
    else if (v <= 1000) step = 100;
    return Math.ceil(v / step) * step;
  }

  function defaultBandRange(bands) {
    if (!bands) return { minY: 0, maxY: 1 };
    if (bands.norm_max > bands.norm_min) {
      return { minY: bands.norm_min, maxY: bands.norm_max };
    }
    return { minY: 0, maxY: 1 };
  }

  function bounds(points) {
    var minT = points[0].t, maxT = points[0].t, minY = points[0].v, maxY = points[0].v, i;
    for (i = 0; i < points.length; i++) {
      if (points[i].t < minT) minT = points[i].t;
      if (points[i].t > maxT) maxT = points[i].t;
      if (points[i].v < minY) minY = points[i].v;
      if (points[i].v > maxY) maxY = points[i].v;
    }
    if (maxT === minT) maxT = minT + 1;
    if (maxY === minY) {
      if (minY > 0 && minY < 1) {
        var step = Math.max(0.01, minY * 0.25);
        minY = Math.max(0, minY - step);
        maxY = maxY + step;
      } else {
        maxY = minY + 1;
      }
    }
    return { minT: minT, maxT: maxT, minY: minY, maxY: maxY };
  }

  function bandBounds(points, bands) {
    if (!points || !points.length) {
      var def = defaultBandRange(bands);
      return { minT: 0, maxT: 1, minY: def.minY, maxY: def.maxY };
    }
    var base = bounds(points);
    if (!bands) return base;
    var rawMin = points[0].v, rawMax = points[0].v, i;
    for (i = 0; i < points.length; i++) {
      if (points[i].v < rawMin) rawMin = points[i].v;
      if (points[i].v > rawMax) rawMax = points[i].v;
    }
    var norm = defaultBandRange(bands);
    var minY = norm.minY, maxY = norm.maxY;
    if (bands.floor_zero) {
      minY = 0;
      if (rawMax > maxY * 0.95) {
        maxY = niceTop(rawMax * 1.1);
      }
    } else {
      if (rawMin < minY + 1) minY = Math.floor((rawMin - 2) / 4) * 4;
      if (rawMax > maxY - 1) maxY = Math.ceil((rawMax + 2) / 4) * 4;
      if (bands.clamp_max > 0 && minY < 0) minY = 0;
    }
    if (bands.clamp_max > 0 && maxY > bands.clamp_max) {
      maxY = bands.clamp_max;
    }
    if (maxY <= minY) maxY = minY + 1;
    return { minT: base.minT, maxT: base.maxT, minY: minY, maxY: maxY };
  }

  function zoneOf(v, bands) {
    if (!bands || v == null || isNaN(v)) return "";
    if (bands.two_sided) {
      if (v < bands.low_danger || v >= bands.high) return "danger";
      if (v < bands.low_warn || v > bands.warn) return "warn";
      return "ok";
    }
    if (v >= bands.high) return "danger";
    if (v >= bands.warn) return "warn";
    return "ok";
  }

  function zoneVar(zone) {
    if (zone === "danger") return "var(--danger)";
    if (zone === "warn") return "var(--warn)";
    return "var(--ok)";
  }

  function dotColor(className) {
    if (className === "series-2") return "var(--series-2)";
    if (className === "series-3") return "var(--series-3)";
    return "var(--ok)";
  }

  function pointColor(v, bands, className) {
    if (bands) return zoneVar(zoneOf(v, bands));
    return dotColor(className);
  }

  function bandThresholds(bands, minY, maxY) {
    if (!bands) return [];
    var raw = bands.two_sided ? [
      { value: bands.high, cls: "threshold-danger", tagCls: "threshold-tag-danger", label: num(bands.high) },
      { value: bands.warn, cls: "threshold-warn", tagCls: "threshold-tag-warn", label: num(bands.warn) },
      { value: bands.low_warn, cls: "threshold-warn", tagCls: "threshold-tag-warn", label: num(bands.low_warn) },
      { value: bands.low_danger, cls: "threshold-danger", tagCls: "threshold-tag-danger", label: num(bands.low_danger) }
    ] : [
      { value: bands.high, cls: "threshold-danger", tagCls: "threshold-tag-danger", label: num(bands.high) },
      { value: bands.warn, cls: "threshold-warn", tagCls: "threshold-tag-warn", label: num(bands.warn) }
    ];
    return raw.filter(function (m) { return m.value > minY && m.value < maxY; });
  }

  function gradientStops(minY, maxY, h, pad, bands) {
    if (!bands || maxY <= minY || h <= 0) {
      return [{ offset: 0, color: "var(--ok)" }, { offset: 1, color: "var(--ok)" }];
    }
    var p = pad != null ? pad : 8;
    var inner = h - 2 * p;
    if (inner < 1) { inner = h; p = 0; }
    function off(v) {
      var y = p + ((maxY - v) / (maxY - minY)) * inner;
      var o = y / h;
      if (o < 0) return 0;
      if (o > 1) return 1;
      return o;
    }
    var regions = bands.two_sided ? [
      { lo: bands.high, hi: Infinity, color: "var(--danger)" },
      { lo: bands.warn, hi: bands.high, color: "var(--warn)" },
      { lo: bands.low_warn, hi: bands.warn, color: "var(--ok)" },
      { lo: bands.low_danger, hi: bands.low_warn, color: "var(--warn)" },
      { lo: -Infinity, hi: bands.low_danger, color: "var(--danger)" }
    ] : [
      { lo: bands.high, hi: Infinity, color: "var(--danger)" },
      { lo: bands.warn, hi: bands.high, color: "var(--warn)" },
      { lo: -Infinity, hi: bands.warn, color: "var(--ok)" }
    ];
    var stops = [];
    regions.forEach(function (r) {
      if (r.hi <= minY || r.lo >= maxY) return;
      var topOff = (r.hi !== Infinity && r.hi < maxY) ? off(r.hi) : 0;
      var botOff = (r.lo !== -Infinity && r.lo > minY) ? off(r.lo) : 1;
      stops.push({ offset: topOff, color: r.color });
      stops.push({ offset: botOff, color: r.color });
    });
    if (!stops.length) {
      return [{ offset: 0, color: "var(--ok)" }, { offset: 1, color: "var(--ok)" }];
    }
    return stops;
  }

  function updateBandGradients(svg, strokeId, fillId, minY, maxY, h, pad, bands, fallbackColor) {
    if (!svg) return;
    var stops = bands
      ? gradientStops(minY, maxY, h, pad, bands)
      : [{ offset: 0, color: fallbackColor || "var(--ok)" }, { offset: 1, color: fallbackColor || "var(--ok)" }];
    if (strokeId) {
      var sg = svg.querySelector("#" + strokeId);
      if (sg) {
        sg.setAttribute("gradientUnits", "userSpaceOnUse");
        sg.setAttribute("x1", "0");
        sg.setAttribute("y1", "0");
        sg.setAttribute("x2", "0");
        sg.setAttribute("y2", String(h));
        sg.textContent = "";
        stops.forEach(function (st) {
          var el = ns("stop");
          el.setAttribute("offset", (st.offset * 100).toFixed(2) + "%");
          el.setAttribute("stop-color", st.color);
          sg.appendChild(el);
        });
      }
    }
    if (fillId) {
      var fg = svg.querySelector("#" + fillId);
      if (fg) {
        fg.setAttribute("gradientUnits", "userSpaceOnUse");
        fg.setAttribute("x1", "0");
        fg.setAttribute("y1", "0");
        fg.setAttribute("x2", "0");
        fg.setAttribute("y2", String(h));
        fg.textContent = "";
        stops.forEach(function (st) {
          var el = ns("stop");
          var op = 0.22 * (1 - st.offset) + 0.02;
          el.setAttribute("offset", (st.offset * 100).toFixed(2) + "%");
          el.setAttribute("stop-color", st.color);
          el.setAttribute("stop-opacity", op.toFixed(3));
          fg.appendChild(el);
        });
      }
    }
  }

  function setZoneVisual(dotEl, valEl, zone) {
    if (dotEl) {
      if (!zone) {
        dotEl.hidden = true;
      } else {
        dotEl.hidden = false;
        var extra = dotEl.classList.contains("chart-zone-dot") ? " chart-zone-dot" : "";
        dotEl.className = "dot dot-" + zone + extra;
      }
    }
    if (valEl) {
      valEl.classList.remove("val-ok", "val-warn", "val-danger", "val-muted");
      if (zone) valEl.classList.add("val-" + zone);
    }
  }

  function project(points, w, h, box, pad) {
    var p = pad != null ? pad : 8;
    var inner = Math.max(1, h - p * 2);
    return points.map(function (pt) {
      return {
        x: ((pt.t - box.minT) / (box.maxT - box.minT)) * w,
        y: p + ((box.maxY - pt.v) / (box.maxY - box.minY)) * inner,
        t: pt.t,
        v: pt.v,
        gap: !!pt.gap,
        min: pt.min,
        max: pt.max
      };
    });
  }

  function tangents(xs, ys) {
    var n = xs.length;
    var d = [];
    var m = [];
    var i;
    for (i = 0; i < n - 1; i++) {
      var dx = xs[i + 1] - xs[i];
      d[i] = dx === 0 ? 0 : (ys[i + 1] - ys[i]) / dx;
    }
    m[0] = d[0];
    for (i = 1; i < n - 1; i++) {
      m[i] = d[i - 1] * d[i] <= 0 ? 0 : (d[i - 1] + d[i]) / 2;
    }
    m[n - 1] = d[n - 2];
    for (i = 0; i < n - 1; i++) {
      if (d[i] === 0) { m[i] = 0; m[i + 1] = 0; continue; }
      var alpha = m[i] / d[i];
      var beta = m[i + 1] / d[i];
      var s = alpha * alpha + beta * beta;
      if (s > 9) {
        var tau = 3 / Math.sqrt(s);
        m[i] = tau * alpha * d[i];
        m[i + 1] = tau * beta * d[i];
      }
    }
    return m;
  }

  function pathOf(pts) {
    if (pts.length < 2) return "";
    var xs = pts.map(function (p) { return p.x; });
    var ys = pts.map(function (p) { return p.y; });
    var m = tangents(xs, ys);
    var d = "M " + pts[0].x.toFixed(2) + "," + pts[0].y.toFixed(2);
    for (var i = 0; i < pts.length - 1; i++) {
      var seg = (pts[i + 1].x - pts[i].x) / 3;
      d += " C " + (pts[i].x + seg).toFixed(2) + "," + (pts[i].y + m[i] * seg).toFixed(2);
      d += " " + (pts[i + 1].x - seg).toFixed(2) + "," + (pts[i + 1].y - m[i + 1] * seg).toFixed(2);
      d += " " + pts[i + 1].x.toFixed(2) + "," + pts[i + 1].y.toFixed(2);
    }
    return d;
  }

  function split(pts) {
    var runs = [];
    var run = [];
    pts.forEach(function (p) {
      if (p.gap && run.length) { runs.push(run); run = []; }
      run.push(p);
    });
    if (run.length) runs.push(run);
    return runs;
  }

  function ns(name) { return document.createElementNS("http://www.w3.org/2000/svg", name); }

  function inGap(raw, t) {
    for (var i = 1; i < raw.length; i++) {
      if (raw[i].gap && t > raw[i - 1].t && t < raw[i].t) return true;
    }
    return false;
  }

  function nearestRaw(raw, t) {
    if (!raw.length) return null;
    var best = raw[0];
    var bestD = Math.abs(raw[0].t - t);
    for (var i = 1; i < raw.length; i++) {
      var d = Math.abs(raw[i].t - t);
      if (d < bestD) { best = raw[i]; bestD = d; }
    }
    return best;
  }

  function formatWhen(t, span) {
    var d = new Date(t * 1000);
    if (span === "7d" || span === "30d") {
      return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
    }
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }

  function formatTick(t, span) {
    var d = new Date(t * 1000);
    if (span === "7d" || span === "30d") {
      return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    }
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }

  function valueText(ser, pt, span) {
    if (!pt) return "—";
    if ((span === "7d" || span === "30d") && pt.min != null && pt.max != null) {
      return num(pt.v) + " (" + num(pt.min) + "–" + num(pt.max) + ")";
    }
    return num(pt.v) + (ser.unit ? " " + ser.unit : "");
  }

  function paint(svg, seriesList, w, h, fillId, strokeId, threshCfg) {
    svg.querySelectorAll("path.series, path.area, circle, line.hover-guide, line.threshold-line, text.threshold-tag").forEach(function (n) { n.remove(); });
    svg.querySelectorAll("line.grid").forEach(function (line) { line.setAttribute("x2", String(w)); });

    var tags = [];
    if (threshCfg && threshCfg.bands && threshCfg.maxY > threshCfg.minY) {
      var p = threshCfg.pad != null ? threshCfg.pad : 8;
      var inner = Math.max(1, h - 2 * p);
      var marks = bandThresholds(threshCfg.bands, threshCfg.minY, threshCfg.maxY);
      marks.forEach(function (m) {
        var y = p + ((threshCfg.maxY - m.value) / (threshCfg.maxY - threshCfg.minY)) * inner;
        var line = ns("line");
        line.setAttribute("class", "threshold-line " + m.cls);
        line.setAttribute("x1", "0");
        line.setAttribute("x2", String(w));
        line.setAttribute("y1", y.toFixed(2));
        line.setAttribute("y2", y.toFixed(2));
        svg.appendChild(line);
        tags.push({ y: y, label: m.label, tagCls: m.tagCls });
      });
    }

    seriesList.forEach(function (ser) {
      split(ser.pts).forEach(function (run) {
        var d = pathOf(run);
        if (!d) return;
        if (ser.fill && fillId && run.length >= 2) {
          var area = ns("path");
          area.setAttribute("class", "area");
          area.setAttribute("fill", "url(#" + fillId + ")");
          area.setAttribute("d", d + " L " + run[run.length - 1].x.toFixed(2) + "," + h.toFixed(0) + " L " + run[0].x.toFixed(2) + "," + h.toFixed(0) + " Z");
          svg.appendChild(area);
        }
        var path = ns("path");
        if (strokeId) {
          path.setAttribute("class", "series series-band");
          path.setAttribute("stroke", "url(#" + strokeId + ")");
        } else {
          path.setAttribute("class", "series " + (ser.className || "series-a"));
        }
        path.setAttribute("d", d);
        svg.appendChild(path);
      });
    });

    if (tags.length) {
      var minGap = threshCfg.minTagGap != null ? threshCfg.minTagGap : 14;
      var placedY = [];
      tags.forEach(function (tg) {
        var ty = tg.y < 14 ? tg.y + 10 : tg.y - 3;
        var crowded = placedY.some(function (py) { return Math.abs(py - ty) < minGap; });
        if (crowded) return;
        placedY.push(ty);
        var txt = ns("text");
        txt.setAttribute("class", "threshold-tag " + tg.tagCls);
        txt.setAttribute("x", String(Math.max(4, w - 4)));
        txt.setAttribute("y", ty.toFixed(1));
        txt.setAttribute("text-anchor", "end");
        txt.textContent = tg.label;
        svg.appendChild(txt);
      });
    }
  }

  function renderDataDots(svg, pts, className, radius, bands) {
    if (!pts || !pts.length) return;
    var step = pts.length > 120 ? Math.ceil(pts.length / 120) : 1;
    function addDot(p) {
      var dot = ns("circle");
      dot.setAttribute("class", "data-dot");
      dot.setAttribute("r", String(radius));
      dot.setAttribute("fill", pointColor(p.v, bands, className));
      dot.setAttribute("cx", p.x.toFixed(2));
      dot.setAttribute("cy", p.y.toFixed(2));
      svg.appendChild(dot);
    }
    for (var i = 0; i < pts.length; i += step) {
      addDot(pts[i]);
    }
    if ((pts.length - 1) % step !== 0) {
      addDot(pts[pts.length - 1]);
    }
  }

  function syncTextList(container, items) {
    if (!container || !Array.isArray(items)) return;
    container.textContent = "";
    items.forEach(function (label) {
      var span = document.createElement("span");
      span.textContent = label;
      container.appendChild(span);
    });
  }

  function syncStats(container, stats) {
    if (!container || !Array.isArray(stats)) return;
    container.textContent = "";
    stats.forEach(function (st) {
      var outer = document.createElement("span");
      var l = document.createElement("span");
      l.className = "muted";
      l.textContent = st.label;
      var v = document.createElement("span");
      v.className = "num";
      v.textContent = st.value;
      outer.appendChild(l);
      outer.appendChild(document.createTextNode(" "));
      outer.appendChild(v);
      container.appendChild(outer);
    });
  }

  function yTicksBounds(minY, maxY, count) {
    var steps = Math.max(1, count - 1);
    if (maxY <= minY) maxY = minY + 1;
    var out = [];
    for (var i = steps; i >= 0; i--) {
      out.push(num(minY + (maxY - minY) * (i / steps)));
    }
    return out;
  }

  function xTicksN(points, span, count) {
    if (!points || !points.length) return [];
    var n = Math.min(count, points.length);
    var out = [];
    for (var i = 0; i < n; i++) {
      var idx = n > 1 ? Math.floor(i * (points.length - 1) / (n - 1)) : 0;
      out.push(formatTick(points[idx].t, span));
    }
    return out;
  }

  // ---- Centered Large Zoom Modal ----
  var zoomDialog = document.getElementById("chart-zoom");
  var zoomWrap = document.getElementById("zoom-wrap");
  var zoomSvg = document.getElementById("zoom-svg");
  var zoomY = document.getElementById("zoom-y");
  var zoomX = document.getElementById("zoom-x");
  var zoomTitle = document.getElementById("zoom-title");
  var zoomDesc = document.getElementById("zoom-desc");
  var zoomZoneDot = document.getElementById("zoom-zone-dot");
  var zoomSpan = document.getElementById("zoom-span");
  var zoomVal = document.getElementById("zoom-val");
  var zoomWhen = document.getElementById("zoom-when");
  var zoomEmpty = document.getElementById("zoom-empty");
  var zoomStats = document.getElementById("zoom-stats");
  var zoomBands = document.getElementById("zoom-bands");
  var zoomClose = document.getElementById("zoom-close");

  var zoomState = {
    open: false,
    id: "",
    data: null,
    scrubbing: false,
    rect: { left: 0, width: 880, height: 300 },
    minT: 0,
    maxT: 1,
    series: [],
    liveLabel: "—",
    liveZone: ""
  };

  function spanLabel(s) {
    if (s === "6h") return "6 h";
    if (s === "24h") return "24 h";
    if (s === "7d") return "7 d";
    if (s === "30d") return "30 d";
    return s || "";
  }

  function measureZoom() {
    if (!zoomSvg) return;
    var r = zoomSvg.getBoundingClientRect();
    zoomState.rect = {
      left: r.left,
      width: Math.max(1, Math.round(r.width || 880)),
      height: Math.max(1, Math.round(r.height || 300))
    };
  }

  function drawZoom() {
    if (!zoomState.open || !zoomState.data || !zoomSvg) return;
    var d = zoomState.data;
    var bands = d.bands || null;
    var series = d.series || [];
    var ser0 = series[0] || { name: d.title || "", unit: d.unit || "", class: "series-a", fill: true, points: [] };
    var all = [];
    series.forEach(function (ser) { (ser.points || []).forEach(function (p) { all.push(p); }); });
    var box = bands ? bandBounds(all, bands) : (all.length ? bounds(all) : { minT: 0, maxT: 1, minY: 0, maxY: 1 });

    measureZoom();
    var w = zoomState.rect.width;
    var h = zoomState.rect.height;
    zoomSvg.setAttribute("viewBox", "0 0 " + w + " " + h);
    var gridLines = zoomSvg.querySelectorAll("line.grid");
    for (var g = 0; g < gridLines.length; g++) {
      var gy = 12 + (h - 24) * (g / Math.max(1, gridLines.length - 1));
      gridLines[g].setAttribute("y1", gy.toFixed(1));
      gridLines[g].setAttribute("y2", gy.toFixed(1));
      gridLines[g].setAttribute("x2", String(w));
    }

    var fallbackColor = d.color || dotColor(ser0.class);
    updateBandGradients(zoomSvg, bands ? "stroke-zoom" : "", "fill-zoom", box.minY, box.maxY, h, 12, bands, fallbackColor);

    zoomState.minT = box.minT;
    zoomState.maxT = box.maxT;
    zoomState.series = [];

    var painted = series.map(function (ser) {
      var raw = ser.points || [];
      var pts = project(raw, w, h, box, 12);
      zoomState.series.push({ name: ser.name, unit: ser.unit, className: ser.class || "series-a", pts: pts, raw: raw });
      return { fill: !!ser.fill && !(bands && bands.two_sided), className: ser.class || "series-a", pts: pts };
    });
    paint(zoomSvg, painted, w, h, "fill-zoom", bands ? "stroke-zoom" : "", bands ? { bands: bands, minY: box.minY, maxY: box.maxY, pad: 12, minTagGap: 14 } : null);

    if (zoomTitle) zoomTitle.textContent = d.title || "Chart";
    if (zoomDesc) {
      zoomDesc.textContent = d.meaning || "";
      zoomDesc.hidden = !d.meaning;
    }
    if (zoomSpan) zoomSpan.textContent = spanLabel(d.span);
    if (zoomEmpty) {
      zoomEmpty.textContent = d.empty_text || "Collecting readings…";
      zoomEmpty.hidden = !d.empty;
    }
    syncTextList(zoomY, yTicksBounds(box.minY, box.maxY, 5));
    syncTextList(zoomX, xTicksN(ser0.points || [], d.span, 6));

    var rd = (d.readouts && d.readouts[0] && d.readouts[0].value) || "—";
    var rawPts = ser0.points || [];
    var liveZone = d.zone || (bands && rawPts.length ? zoneOf(rawPts[rawPts.length - 1].v, bands) : "");
    zoomState.liveLabel = rd;
    zoomState.liveZone = liveZone;
    if (zoomVal) zoomVal.textContent = rd;
    if (zoomWhen) zoomWhen.textContent = "Now";
    setZoneVisual(zoomZoneDot, zoomVal, liveZone);
    if (zoomBands) {
      if (bands && bands.legend) {
        zoomBands.textContent = bands.legend;
        zoomBands.hidden = false;
      } else {
        zoomBands.textContent = "";
        zoomBands.hidden = true;
      }
    }
    if (zoomWrap && d.aria_label) zoomWrap.setAttribute("aria-label", d.aria_label);

    var stats = Array.isArray(d.stats) ? d.stats.slice() : [];
    stats.push({ label: "Samples", value: String(rawPts.length) });
    syncStats(zoomStats, stats);
  }

  function showZoomAt(t) {
    if (!zoomState.open || !zoomState.data || !zoomSvg) return;
    zoomState.scrubbing = true;
    zoomWrap.setAttribute("data-scrub", "1");
    var d = zoomState.data;
    var bands = d.bands || null;
    if (zoomWhen) zoomWhen.textContent = formatWhen(t, d.span);
    zoomSvg.querySelectorAll("line.hover-guide, circle").forEach(function (n) { n.remove(); });
    var h = zoomState.rect.height;

    var texts = [];
    var activeZone = bands ? "muted" : "";
    zoomState.series.forEach(function (ser, idx) {
      if (inGap(ser.raw, t)) {
        texts.push("—");
        return;
      }
      var pt = nearestRaw(ser.pts, t);
      if (!pt) return;
      texts.push(valueText(ser, pt, d.span));
      if (idx === 0) {
        if (bands) activeZone = zoneOf(pt.v, bands);
        var guide = ns("line");
        guide.setAttribute("class", "hover-guide");
        guide.setAttribute("x1", pt.x.toFixed(2));
        guide.setAttribute("x2", pt.x.toFixed(2));
        guide.setAttribute("y1", "0");
        guide.setAttribute("y2", String(h));
        zoomSvg.appendChild(guide);
      }
    });
    if (zoomVal) zoomVal.textContent = texts.length ? texts.join(" · ") : "—";
    setZoneVisual(zoomZoneDot, zoomVal, activeZone);
  }

  function clearZoomHover() {
    zoomState.scrubbing = false;
    if (zoomWrap) {
      zoomWrap.removeAttribute("data-scrub");
      zoomWrap.removeAttribute("data-i");
    }
    if (zoomSvg) {
      zoomSvg.querySelectorAll("line.hover-guide, circle").forEach(function (n) { n.remove(); });
    }
    if (zoomVal) zoomVal.textContent = zoomState.liveLabel;
    if (zoomWhen) zoomWhen.textContent = "Now";
    setZoneVisual(zoomZoneDot, zoomVal, zoomState.liveZone);
  }

  function openZoom(inst) {
    if (!zoomDialog || !inst) return;
    zoomState.open = true;
    zoomState.id = inst.id;
    zoomState.data = inst.getData();
    zoomState.scrubbing = false;
    if (typeof zoomDialog.showModal === "function") {
      if (!zoomDialog.open) zoomDialog.showModal();
    } else {
      zoomDialog.setAttribute("open", "");
    }
    drawZoom();
    window.dispatchEvent(new CustomEvent("dmd-zoom-open", {
      detail: { id: inst.id, title: zoomState.data.title || inst.id, span: zoomState.data.span || "6h" }
    }));
  }

  function closeZoom() {
    zoomState.open = false;
    clearZoomHover();
    if (zoomDialog && zoomDialog.open) {
      if (typeof zoomDialog.close === "function") zoomDialog.close();
      else zoomDialog.removeAttribute("open");
    }
  }

  if (zoomDialog && zoomWrap) {
    if (zoomClose) zoomClose.addEventListener("click", closeZoom);
    zoomDialog.addEventListener("click", function (ev) {
      if (ev.target === zoomDialog) closeZoom();
    });
    zoomDialog.addEventListener("close", function () {
      zoomState.open = false;
      clearZoomHover();
    });
    if (window.ResizeObserver) {
      var zro = new ResizeObserver(function () { if (zoomState.open && !zoomState.scrubbing) drawZoom(); });
      zro.observe(zoomWrap);
    }
    zoomWrap.addEventListener("pointerenter", measureZoom);
    zoomWrap.addEventListener("pointermove", function (ev) {
      if (!zoomState.rect.width) return;
      var x = Math.min(1, Math.max(0, (ev.clientX - zoomState.rect.left) / zoomState.rect.width));
      showZoomAt(zoomState.minT + x * (zoomState.maxT - zoomState.minT));
    });
    zoomWrap.addEventListener("pointerleave", clearZoomHover);
    zoomWrap.addEventListener("keydown", function (ev) {
      var raw = (zoomState.series[0] && zoomState.series[0].raw) || [];
      if (!raw.length) return;
      var cur = Number(zoomWrap.getAttribute("data-i") || (raw.length - 1));
      if (ev.key === "ArrowLeft") cur = Math.max(0, cur - 1);
      else if (ev.key === "ArrowRight") cur = Math.min(raw.length - 1, cur + 1);
      else if (ev.key === "Home") cur = 0;
      else if (ev.key === "End") cur = raw.length - 1;
      else return;
      ev.preventDefault();
      zoomWrap.setAttribute("data-i", String(cur));
      showZoomAt(raw[cur].t);
    });
  }

  // ---- Grid Chart Cards ----
  function mount(card) {
    var id = card.getAttribute("data-chart") || "";
    var node = card.querySelector("template.chart-data");
    var wrap = card.querySelector(".chart-wrap");
    if (!node || !wrap) return null;
    var data = {};
    var rawJSON = (node.content ? node.content.textContent : node.textContent) || "{}";
    try { data = JSON.parse(rawJSON); } catch (e) { data = {}; }
    var cache = { minT: 0, maxT: 1, svgs: [], series: [] };
    var liveText = {};
    var liveZone = {};
    var cardLiveZone = "";
    var zoneDot = card.querySelector(".chart-zone-dot");

    function measure() {
      var svgs = Array.prototype.slice.call(wrap.querySelectorAll("svg.chart-svg"));
      cache.svgs = svgs.map(function (svg, idx) {
        var r = svg.getBoundingClientRect();
        var fallbackH = data.compact ? 108 : (svgs.length > 1 ? 64 : 136);
        return {
          svg: svg,
          left: r.left,
          width: Math.max(1, Math.round(r.width || wrap.getBoundingClientRect().width || 1)),
          height: Math.max(1, Math.round(r.height || fallbackH)),
          fillId: svg.getAttribute("data-fill-id") || ("fill-" + (id || "chart") + "-" + idx),
          strokeId: svg.getAttribute("data-stroke-id") || ""
        };
      });
    }

    function syncMeta() {
      var plot = card.querySelector(".chart-plot");
      var empty = !!data.empty;
      if (plot) plot.setAttribute("data-empty", empty ? "1" : "0");
      var emptyEl = card.querySelector(".chart-empty");
      if (emptyEl) {
        emptyEl.textContent = data.empty_text || "Collecting readings…";
        emptyEl.hidden = !empty;
      }

      if (data.aria_label) wrap.setAttribute("aria-label", data.aria_label);

      if (Array.isArray(data.tracks)) {
        var trackNodes = card.querySelectorAll(".track .track-y");
        data.tracks.forEach(function (tr, i) {
          if (trackNodes[i] && Array.isArray(tr.y_ticks)) {
            syncTextList(trackNodes[i], tr.y_ticks);
          }
        });
      }

      if (Array.isArray(data.x_ticks)) {
        syncTextList(card.querySelector(".chart-x"), data.x_ticks);
      }

      var bands = data.bands || null;
      cardLiveZone = data.zone || "";
      if (Array.isArray(data.readouts)) {
        liveText = {};
        liveZone = {};
        data.readouts.forEach(function (item, i) {
          liveText[item.label] = item.value;
          var z = item.zone || "";
          if (!z && bands && data.series && data.series[i] && data.series[i].points && data.series[i].points.length) {
            var pts = data.series[i].points;
            z = zoneOf(pts[pts.length - 1].v, bands);
          }
          liveZone[item.label] = z;
          if (i === 0 && !cardLiveZone) cardLiveZone = z;
          var el = card.querySelector('.chart-readout [data-readout="' + item.label + '"]');
          if (el) {
            el.textContent = item.value;
            setZoneVisual(null, el, z);
          }
        });
      } else {
        card.querySelectorAll(".chart-readout [data-readout]").forEach(function (el) {
          liveText[el.getAttribute("data-readout")] = el.textContent;
        });
      }
      setZoneVisual(zoneDot, null, cardLiveZone);

      syncStats(card.querySelector(".stats"), data.stats);
    }

    function draw() {
      measure();
      var series = data.series || [];
      var bands = data.bands || null;
      var all = [];
      series.forEach(function (ser) { (ser.points || []).forEach(function (p) { all.push(p); }); });
      var box = all.length ? bounds(all) : { minT: 0, maxT: 1, minY: 0, maxY: 1 };
      var overlay = !!data.overlay || (cache.svgs.length === 1 && series.length > 1);
      cache.minT = box.minT;
      cache.maxT = box.maxT;
      cache.series = [];

      if (overlay && cache.svgs[0]) {
        var info = cache.svgs[0];
        info.svg.setAttribute("viewBox", "0 0 " + info.width + " " + info.height);
        var painted = series.map(function (ser) {
          var raw = ser.points || [];
          var pts = project(raw, info.width, info.height, box);
          cache.series.push({ name: ser.name, unit: ser.unit, className: ser.class || "series-a", pts: pts, raw: raw });
          return { fill: !!ser.fill, className: ser.class || "series-a", pts: pts };
        });
        paint(info.svg, painted, info.width, info.height, info.fillId, "", null);
      } else {
        var trackNodes = card.querySelectorAll(".track .track-y");
        cache.svgs.forEach(function (info, index) {
          info.svg.setAttribute("viewBox", "0 0 " + info.width + " " + info.height);
          var ser = series[index];
          if (!ser) return;
          var raw = ser.points || [];
          var own = bands ? bandBounds(raw, bands) : (raw.length ? bounds(raw) : box);
          var scale = { minT: box.minT, maxT: box.maxT, minY: own.minY, maxY: own.maxY };
          var pts = project(raw, info.width, info.height, scale);
          cache.series.push({ name: ser.name, unit: ser.unit, className: ser.class || "series-a", pts: pts, raw: raw });
          if (bands) {
            updateBandGradients(info.svg, info.strokeId, info.fillId, scale.minY, scale.maxY, info.height, 8, bands, dotColor(ser.class));
            paint(info.svg, [{ fill: !!ser.fill && !bands.two_sided, className: ser.class || "series-a", pts: pts }], info.width, info.height, info.fillId, info.strokeId, { bands: bands, minY: scale.minY, maxY: scale.maxY, pad: 8, minTagGap: 15 });
            var tickCount = (data.compact || cache.svgs.length > 1) ? 3 : 4;
            if (trackNodes[index]) syncTextList(trackNodes[index], yTicksBounds(scale.minY, scale.maxY, tickCount));
          } else {
            paint(info.svg, [{ fill: !!ser.fill, className: ser.class || "series-a", pts: pts }], info.width, info.height, info.fillId, "", null);
          }
        });
      }
      syncMeta();
    }

    function showAt(t, withDots) {
      var bands = data.bands || null;
      var when = card.querySelector(".chart-when");
      if (when) when.textContent = formatWhen(t, data.span);
      var firstZone = bands ? "muted" : "";
      (data.series || []).forEach(function (ser, idx) {
        var el = card.querySelector('.chart-readout [data-readout="' + ser.name + '"]');
        if (!el) return;
        if (inGap(ser.points || [], t)) {
          el.textContent = "—";
          if (bands) setZoneVisual(null, el, "muted");
          return;
        }
        var pt = nearestRaw(ser.points || [], t);
        el.textContent = valueText(ser, pt, data.span);
        if (bands) {
          var z = pt ? zoneOf(pt.v, bands) : "muted";
          if (idx === 0) firstZone = z;
          setZoneVisual(null, el, z);
        }
      });
      if (bands) setZoneVisual(zoneDot, null, firstZone);

      var overlay = !!data.overlay || cache.svgs.length === 1;
      cache.svgs.forEach(function (info, si) {
        var list = overlay ? cache.series : (cache.series[si] ? [cache.series[si]] : []);
        info.svg.querySelectorAll("line.hover-guide, circle").forEach(function (n) { n.remove(); });
        list.forEach(function (ser, idx) {
          if (!ser) return;
          if (inGap(ser.raw, t)) return;
          var pt = nearestRaw(ser.pts, t);
          if (!pt) return;
          if (idx === 0) {
            var guide = ns("line");
            guide.setAttribute("class", "hover-guide");
            guide.setAttribute("x1", pt.x.toFixed(2));
            guide.setAttribute("x2", pt.x.toFixed(2));
            guide.setAttribute("y1", "0");
            guide.setAttribute("y2", String(info.height));
            info.svg.appendChild(guide);
          }
        });
      });
    }

    function clientT(clientX) {
      var info = cache.svgs[0];
      if (!info || !info.width) return cache.minT;
      var x = Math.min(1, Math.max(0, (clientX - info.left) / info.width));
      return cache.minT + x * (cache.maxT - cache.minT);
    }

    function clearHover() {
      wrap.removeAttribute("data-scrub");
      wrap.removeAttribute("data-i");
      cache.svgs.forEach(function (info) {
        info.svg.querySelectorAll("line.hover-guide, circle").forEach(function (n) { n.remove(); });
      });
      var when = card.querySelector(".chart-when");
      if (when) when.textContent = "Now";
      Object.keys(liveText).forEach(function (name) {
        var el = card.querySelector('.chart-readout [data-readout="' + name + '"]');
        if (el) {
          el.textContent = liveText[name];
          setZoneVisual(null, el, liveZone[name] || "");
        }
      });
      setZoneVisual(zoneDot, null, cardLiveZone);
    }

    function setData(next) {
      if (next) data = next;
      draw();
    }

    return {
      id: id,
      card: card,
      wrap: wrap,
      draw: draw,
      measure: measure,
      showAt: showAt,
      clientT: clientT,
      clearHover: clearHover,
      setData: setData,
      getData: function () { return data; },
      primaryPoints: function () { return (data.series && data.series[0] && data.series[0].points) || []; }
    };
  }

  var activeScrub = false;

  function showAll(t, sourceInst) {
    activeScrub = true;
    mounted.forEach(function (c) {
      c.wrap.setAttribute("data-scrub", "1");
      c.showAt(t, c === sourceInst);
    });
  }

  function clearAll() {
    activeScrub = false;
    mounted.forEach(function (c) { c.clearHover(); });
  }

  cards.forEach(function (card) {
    var inst = mount(card);
    if (!inst) return;
    mounted.push(inst);
    inst.draw();

    if (window.ResizeObserver) {
      var ro = new ResizeObserver(function () { if (!activeScrub) inst.draw(); });
      ro.observe(inst.wrap);
    }

    var zoomBtn = card.querySelector(".chart-zoom-btn");
    if (zoomBtn) {
      zoomBtn.addEventListener("click", function (ev) {
        ev.stopPropagation();
        openZoom(inst);
      });
    }

    inst.wrap.addEventListener("click", function () {
      openZoom(inst);
    });
    inst.wrap.addEventListener("pointerenter", function () {
      mounted.forEach(function (c) { c.measure(); });
    });
    inst.wrap.addEventListener("pointermove", function (ev) {
      showAll(inst.clientT(ev.clientX), inst);
    });
    inst.wrap.addEventListener("pointerleave", clearAll);
    inst.wrap.addEventListener("keydown", function (ev) {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        openZoom(inst);
        return;
      }
      var raw = inst.primaryPoints();
      if (!raw.length) return;
      var cur = Number(inst.wrap.getAttribute("data-i") || (raw.length - 1));
      if (ev.key === "ArrowLeft") cur = Math.max(0, cur - 1);
      else if (ev.key === "ArrowRight") cur = Math.min(raw.length - 1, cur + 1);
      else if (ev.key === "Home") cur = 0;
      else if (ev.key === "End") cur = raw.length - 1;
      else if (ev.key === "Escape") { clearAll(); return; }
      else return;
      ev.preventDefault();
      inst.wrap.setAttribute("data-i", String(cur));
      showAll(raw[cur].t, inst);
    });
  });

  window.addEventListener("dmd-history", function (ev) {
    if (activeScrub || !ev.detail) return;
    var detail = ev.detail;
    if (Array.isArray(detail.charts) && detail.charts.length) {
      var byId = {};
      detail.charts.forEach(function (ch) { byId[ch.id] = ch; });
      mounted.forEach(function (c) {
        if (byId[c.id]) c.setData(byId[c.id]);
      });
      if (zoomState.open && !zoomState.scrubbing && byId[zoomState.id]) {
        zoomState.data = byId[zoomState.id];
        drawZoom();
      }
      return;
    }
    if (mounted[0]) mounted[0].setData(detail);
  });
})();
