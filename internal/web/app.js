(function () {
  var token = (document.querySelector('meta[name="csrf-token"]') || {}).content || "";
  var params = new URLSearchParams(location.search);
  var metric = params.get("metric") || "";
  var span = params.get("span") || "6h";

  function overviewURL() {
    var q = new URLSearchParams();
    if (metric) q.set("metric", metric);
    q.set("span", span);
    return "/?" + q.toString();
  }

  function apiQuery() {
    var q = new URLSearchParams();
    if (metric) q.set("metric", metric);
    q.set("span", span);
    return q.toString();
  }

  function syncFanButtons(form, val) {
    if (!form) return;
    var off = form.getAttribute("data-off") === "1";
    var min = parseInt(form.getAttribute("data-min") || "1", 10);
    var max = parseInt(form.getAttribute("data-max") || "5", 10);
    var decBtn = form.querySelector('[data-dir="-1"]');
    var incBtn = form.querySelector('[data-dir="1"]');
    if (decBtn) decBtn.disabled = off || val <= min;
    if (incBtn) incBtn.disabled = off || val >= max;
  }

  function formatHeaterTemp(c, unit) {
    if (c < 0) return "Off";
    if (String(unit || "C").toUpperCase() === "F") {
      return Math.round(c * 9 / 5 + 32) + " °F (" + c + " °C)";
    }
    return c + " °C";
  }

  function syncHeaterButtons(form, val) {
    if (!form) return;
    var off = form.getAttribute("data-off") === "1";
    var min = parseInt(form.getAttribute("data-min") || "18", 10);
    var max = parseInt(form.getAttribute("data-max") || "40", 10);
    var active = val >= min && val <= max;
    var offBtn = form.querySelector('[data-dir="off"]');
    var decBtn = form.querySelector('[data-dir="-1"]');
    var incBtn = form.querySelector('[data-dir="1"]');
    if (offBtn) offBtn.disabled = off || !active;
    if (decBtn) decBtn.disabled = off || !active || val <= min;
    if (incBtn) incBtn.disabled = off || (active && val >= max);
  }

  document.querySelectorAll("form").forEach(function (form) {
    if (!form.querySelector("[data-busy]")) return;
    form.addEventListener("submit", function (ev) {
      if (form.getAttribute("data-js") === "1") return;
      if (form.getAttribute("data-submitting") === "1") {
        ev.preventDefault();
        return;
      }
      var btn = (ev.submitter && ev.submitter.hasAttribute("data-busy")) ? ev.submitter : form.querySelector("[data-busy]");
      if (!btn) return;
      form.setAttribute("data-submitting", "1");
      btn.textContent = btn.getAttribute("data-busy");
      // Defer disabling so the browser includes named submit buttons in the POST body.
      setTimeout(function () { btn.disabled = true; }, 0);
    });
  });

  var email = document.getElementById("email");
  if (email && window.matchMedia("(min-width: 601px)").matches) email.focus();

  function clearStaged() {
    document.querySelectorAll(".is-staged").forEach(function (el) { el.classList.remove("is-staged"); });
    document.querySelectorAll("[data-staged]").forEach(function (el) {
      el.removeAttribute("data-staged");
      var kind = el.getAttribute("data-stepper");
      if (kind === "fan") {
        syncFanButtons(el, parseInt(el.getAttribute("data-cur") || "1", 10));
      } else if (kind === "heater") {
        syncHeaterButtons(el, parseInt(el.getAttribute("data-cur") || "-1", 10));
      }
    });
  }

  function removeConfirm() {
    document.querySelectorAll(".confirm.js").forEach(function (el) { el.remove(); });
  }

  function showError(host, message) {
    var old = host.querySelector(".banner.flash.js");
    if (old) old.remove();
    var flash = document.createElement("p");
    flash.className = "banner flash js";
    flash.setAttribute("role", "status");
    flash.textContent = message;
    host.insertBefore(flash, host.firstChild);
  }

  function confirmBar(text, fields) {
    var host = document.getElementById("controls") || document.querySelector(".lead");
    if (!host) return;
    removeConfirm();
    var box = document.createElement("div");
    box.className = "confirm js";
    box.setAttribute("role", "status");
    var p = document.createElement("p");
    p.textContent = text;
    box.appendChild(p);
    var form = document.createElement("form");
    form.method = "post";
    form.action = "/control";
    form.setAttribute("data-js", "1");
    function hidden(name, value) {
      var input = document.createElement("input");
      input.type = "hidden";
      input.name = name;
      input.value = value;
      form.appendChild(input);
    }
    hidden("token", token);
    hidden("confirmed", "1");
    hidden("page", location.pathname === "/device" ? "device" : "overview");
    if (metric) hidden("metric", metric);
    hidden("span", span);
    Object.keys(fields).forEach(function (k) { hidden(k, fields[k]); });
    var verb = fields.verb || "Confirm change";
    var button = document.createElement("button");
    button.className = "btn btn-primary";
    button.type = "submit";
    button.textContent = verb;
    form.appendChild(button);
    box.appendChild(form);
    var cancel = document.createElement("button");
    cancel.className = "btn";
    cancel.type = "button";
    cancel.textContent = "Cancel";
    box.appendChild(cancel);
    host.appendChild(box);
    cancel.addEventListener("click", function () {
      clearStaged();
      box.remove();
    });
    button.addEventListener("click", function (ev) {
      ev.preventDefault();
      button.disabled = true;
      button.textContent = "Sending…";
      var body = new URLSearchParams(new FormData(form));
      fetch("/control", {
        method: "POST",
        headers: { "Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded" },
        body: body
      }).then(function (res) {
        return res.text().then(function (text) {
          var payload = {};
          try { payload = JSON.parse(text); } catch (e) { payload = { error: text || "Command failed" }; }
          return { res: res, payload: payload };
        });
      }).then(function (out) {
        if (!out.res.ok) {
          var err = new Error((out.payload && out.payload.error) || "Command failed");
          err.status = out.res.status;
          throw err;
        }
        location.href = location.pathname === "/device" ? "/device" : overviewURL();
      }).catch(function (err) {
        if (err && err.status === 409) {
          clearStaged();
          box.remove();
          fetchState();
          showError(host, err.message);
          return;
        }
        button.disabled = false;
        button.textContent = verb;
        showError(host, err.message);
      });
    });
  }

  document.addEventListener("submit", function (ev) {
    var form = ev.target;
    if (!(form instanceof HTMLFormElement)) return;
    if ((form.method || "").toLowerCase() !== "get") return;
    var action = form.getAttribute("action") || "";
    if (action !== "/" && action !== "/device") return;
    var submitter = ev.submitter;
    if (!submitter || submitter.name !== "stage" || submitter.value === "logout" || submitter.value === "ota_update") return;
    ev.preventDefault();
    if (submitter.disabled || submitter.getAttribute("aria-pressed") === "true") return;

    var stepper = form.getAttribute("data-stepper") || "";
    var isFan = stepper === "fan";
    var isHeater = stepper === "heater";
    var prevStaged = (isFan || isHeater) ? form.getAttribute("data-staged") : null;
    clearStaged();

    var stage = submitter.value || "";
    var bits = stage.split(":");
    var text = submitter.getAttribute("data-confirm") || "";
    var verb = submitter.getAttribute("data-verb") || "Confirm change";
    var fields = {
      action: "set",
      siid: "2",
      piid: "3",
      value: bits[1] || "",
      expected_old: submitter.getAttribute("data-old") || "",
      verb: verb
    };

    if (isFan) {
      var baseCur = parseInt(form.getAttribute("data-cur") || "0", 10);
      var cur = parseInt(prevStaged || String(baseCur), 10);
      var dir = parseInt(submitter.getAttribute("data-dir") || "0", 10);
      var min = parseInt(form.getAttribute("data-min") || "1", 10);
      var max = parseInt(form.getAttribute("data-max") || "5", 10);
      var next = cur + (dir || 0);
      if (next < min) next = min;
      if (next > max) next = max;
      if (next === baseCur) {
        syncFanButtons(form, baseCur);
        removeConfirm();
        return;
      }
      submitter.classList.add("is-staged");
      form.setAttribute("data-staged", String(next));
      syncFanButtons(form, next);
      text = "Fan speed: " + baseCur + " -> " + next + ".";
      if (form.getAttribute("data-forces") === "1") {
        text += " Mode changes from " + (form.getAttribute("data-mode") || "the current mode") + " to Custom.";
      }
      fields.siid = "2";
      fields.piid = "4";
      fields.value = String(next);
      fields.expected_old = String(baseCur);
      fields.verb = "Confirm change";
      confirmBar(text, fields);
      return;
    }

    if (isHeater) {
      var hBase = parseInt(form.getAttribute("data-cur") || "-1", 10);
      var hCur = parseInt(prevStaged !== null ? prevStaged : String(hBase), 10);
      var hDir = submitter.getAttribute("data-dir") || "";
      var hMin = parseInt(form.getAttribute("data-min") || "18", 10);
      var hMax = parseInt(form.getAttribute("data-max") || "40", 10);
      var hUnit = form.getAttribute("data-unit") || "C";
      var hNext = hCur;
      if (hDir === "off") {
        hNext = -1;
      } else if (hDir === "1") {
        if (hCur < hMin) hNext = Math.min(hMax, Math.max(hMin, 22));
        else hNext = Math.min(hMax, hCur + 1);
      } else if (hDir === "-1") {
        hNext = Math.max(hMin, hCur - 1);
      }
      if (hNext === hBase) {
        syncHeaterButtons(form, hBase);
        removeConfirm();
        return;
      }
      submitter.classList.add("is-staged");
      form.setAttribute("data-staged", String(hNext));
      syncHeaterButtons(form, hNext);
      if (hNext < 0) {
        text = "Turn heater off? (" + formatHeaterTemp(hBase, hUnit) + " -> Off)";
      } else {
        text = "Heater: " + formatHeaterTemp(hBase, hUnit) + " -> " + formatHeaterTemp(hNext, hUnit) + ".";
      }
      fields.siid = "2";
      fields.piid = "6";
      fields.value = String(hNext);
      fields.expected_old = String(hBase);
      fields.verb = "Confirm change";
      confirmBar(text, fields);
      return;
    }

    submitter.classList.add("is-staged");
    if (bits[0] === "power") {
      fields = {
        action: "toggle_power",
        siid: "2",
        piid: "1",
        value: bits[1] || "1",
        expected_old: submitter.getAttribute("data-old") || (bits[1] === "2" ? "2" : "1"),
        verb: verb
      };
    } else if (bits[0] && bits[0].indexOf(".") >= 0) {
      var id = bits[0].split(".");
      fields.siid = id[0];
      fields.piid = id[1];
      if (!text) text = "Change " + (submitter.getAttribute("data-label") || bits[0]) + " to " + (bits[1] || "") + ".";
    }
    confirmBar(text, fields);
  });

  document.addEventListener("keydown", function (ev) {
    if (ev.key !== "Escape") return;
    if (!document.querySelector(".confirm.js")) return;
    clearStaged();
    removeConfirm();
  });

  var filter = document.getElementById("activity-filter");
  var body = document.getElementById("activity-body");
  var count = document.getElementById("activity-count");
  if (filter && body && count) {
    filter.addEventListener("input", function () {
      var q = filter.value.trim().toLowerCase();
      var rows = body.querySelectorAll("tr[data-filter]");
      var shown = 0;
      rows.forEach(function (row) {
        var ok = !q || (row.getAttribute("data-filter") || "").indexOf(q) >= 0;
        row.hidden = !ok;
        if (ok) shown++;
      });
      count.textContent = "Showing " + shown + " of " + rows.length;
    });
  }

  var live = document.getElementById("live");
  var announcer = document.getElementById("announcer");
  var age = document.getElementById("age");
  var lastWord = age ? age.textContent.split(",")[0] : "";

  function applyState(s) {
    if (!s) return;
    var lead = document.getElementById("lead");
    if (lead) {
      lead.textContent = !s.lead || s.lead === "—" ? "—" : s.lead + "\u00a0µg/m³";
    }
    var pill = document.getElementById("aq-pill");
    if (pill) {
      pill.textContent = s.pill || "";
      pill.className = "pill pill-" + (s.pill_class || "muted");
    }
    var row = document.getElementById("lead-row");
    if (row) row.className = "exit aq-" + (s.pill_class || "muted");
    var sentence = document.getElementById("sentence");
    if (sentence && s.sentence) sentence.textContent = s.sentence;
    (s.sensors || []).forEach(function (item) {
      var el = document.querySelector('[data-sensor="' + item.label + '"]');
      if (el) el.textContent = item.value;
    });
    var hepaLabel = document.getElementById("hepa-label");
    if (hepaLabel) hepaLabel.textContent = s.hepa_known ? (s.hepa_label || s.hepa) + "%" : "—";
    var hepaBar = document.getElementById("hepa-bar");
    if (hepaBar) {
      if (s.hepa_known) hepaBar.setAttribute("aria-valuenow", String(s.hepa));
      else hepaBar.removeAttribute("aria-valuenow");
    }
    var hepaFill = document.getElementById("hepa-fill");
    if (hepaFill) {
      hepaFill.setAttribute("class", "rail-fill rail-" + (s.hepa_class || "muted"));
      hepaFill.setAttribute("width", String(s.hepa_known ? s.hepa : 0));
    }
    var hepaText = document.getElementById("hepa-text");
    if (hepaText && s.hepa_text) hepaText.textContent = s.hepa_text;
    var carbonLabel = document.getElementById("carbon-label");
    if (carbonLabel) carbonLabel.textContent = s.carbon_known ? (s.carbon_label || s.carbon) + "%" : "—";
    var carbonBar = document.getElementById("carbon-bar");
    if (carbonBar) {
      if (s.carbon_known) carbonBar.setAttribute("aria-valuenow", String(s.carbon));
      else carbonBar.removeAttribute("aria-valuenow");
    }
    var carbonFill = document.getElementById("carbon-fill");
    if (carbonFill) {
      carbonFill.setAttribute("class", "rail-fill rail-" + (s.carbon_class || "muted"));
      carbonFill.setAttribute("width", String(s.carbon_known ? s.carbon : 0));
    }
    var carbonText = document.getElementById("carbon-text");
    if (carbonText && s.carbon_text) carbonText.textContent = s.carbon_text;
    if (age && s.age) {
      var word = String(s.age).split(",")[0];
      if (announcer && word && word !== lastWord) {
        announcer.textContent = word;
        lastWord = word;
      }
      age.textContent = s.age;
    }
    var dot = document.querySelector(".conn .dot");
    if (dot && s.dot) dot.className = "dot dot-" + s.dot;
    var topStandby = document.getElementById("top-standby");
    if (topStandby) topStandby.hidden = !s.standby;
    var powerPill = document.getElementById("power-state-pill");
    if (powerPill) {
      powerPill.textContent = s.power_state || "";
      powerPill.className = "pill pill-" + (s.power_state_class || "muted");
      powerPill.hidden = !s.power_state;
    }
    var writeReason = document.getElementById("write-reason");
    if (writeReason) {
      writeReason.textContent = s.write_reason || "";
      writeReason.hidden = !s.write_reason;
    }
    if (s.standby) {
      var stagedActive = document.querySelector(
        'form[data-stepper="fan"][data-staged], form[data-stepper="heater"][data-staged], ' +
        'button[name="stage"][value^="2.3:"].is-staged, button[name="stage"][value^="2.7:"].is-staged, ' +
        'button[name="stage"][value^="6.20:"].is-staged'
      );
      if (stagedActive) {
        clearStaged();
        removeConfirm();
      }
    }
    var power = document.getElementById("power-btn");
    if (power && !power.classList.contains("is-staged")) {
      power.textContent = s.power_label || power.textContent;
      power.value = s.power_stage || power.value;
      power.disabled = !!s.power_off;
      if (s.power_old) power.setAttribute("data-old", s.power_old);
      if (s.power_verb) power.setAttribute("data-verb", s.power_verb);
      if (s.power_text) power.setAttribute("data-confirm", s.power_text);
    }
    (s.controls || []).forEach(function (c) {
      var btn = document.querySelector('button[name="stage"][value="' + c.stage + '"]');
      if (!btn || btn.classList.contains("is-staged")) return;
      if (btn.hasAttribute("aria-pressed")) btn.setAttribute("aria-pressed", c.pressed ? "true" : "false");
      btn.disabled = !!c.off;
      if (c.old != null) btn.setAttribute("data-old", String(c.old));
      if (c.confirm) btn.setAttribute("data-confirm", c.confirm);
    });
    if (s.fan) {
      var fanForm = document.querySelector('form[data-stepper="fan"]');
      if (fanForm && !fanForm.hasAttribute("data-staged")) {
        fanForm.setAttribute("data-cur", String(s.fan.cur));
        fanForm.setAttribute("data-min", String(s.fan.min));
        fanForm.setAttribute("data-max", String(s.fan.max));
        fanForm.setAttribute("data-forces", s.fan.forces ? "1" : "0");
        fanForm.setAttribute("data-mode", s.fan.mode_name || "");
        fanForm.setAttribute("data-off", s.fan.off ? "1" : "0");
        var fanNow = fanForm.querySelector(".fan-now");
        if (fanNow && s.fan.text) fanNow.textContent = s.fan.text;
        var decBtn = fanForm.querySelector('[data-dir="-1"]');
        if (decBtn) {
          decBtn.value = s.fan.dec || decBtn.value;
          decBtn.setAttribute("data-old", String(s.fan.cur));
          decBtn.disabled = !!s.fan.dec_off || !!s.fan.off;
        }
        var incBtn = fanForm.querySelector('[data-dir="1"]');
        if (incBtn) {
          incBtn.value = s.fan.inc || incBtn.value;
          incBtn.setAttribute("data-old", String(s.fan.cur));
          incBtn.disabled = !!s.fan.inc_off || !!s.fan.off;
        }
        var fanNote = fanForm.parentElement && fanForm.parentElement.querySelector(".fan-note");
        if (fanNote) {
          fanNote.textContent = s.fan.note || "";
          fanNote.hidden = !s.fan.note;
        }
      }
    }
    if (s.heater) {
      var heaterForm = document.querySelector('form[data-stepper="heater"]');
      if (heaterForm && !heaterForm.hasAttribute("data-staged")) {
        heaterForm.setAttribute("data-cur", String(s.heater.cur));
        heaterForm.setAttribute("data-min", String(s.heater.min));
        heaterForm.setAttribute("data-max", String(s.heater.max));
        heaterForm.setAttribute("data-unit", s.heater.unit || "C");
        heaterForm.setAttribute("data-off", s.heater.off ? "1" : "0");
        var heaterNow = heaterForm.querySelector(".heater-now");
        if (heaterNow && s.heater.text) heaterNow.textContent = s.heater.text;
        var hOffBtn = heaterForm.querySelector('[data-dir="off"]');
        if (hOffBtn) {
          hOffBtn.value = s.heater.off_stage || hOffBtn.value;
          hOffBtn.setAttribute("data-old", String(s.heater.cur));
          hOffBtn.disabled = !!s.heater.off_btn_off || !!s.heater.off;
        }
        var hDecBtn = heaterForm.querySelector('[data-dir="-1"]');
        if (hDecBtn) {
          hDecBtn.value = s.heater.dec || hDecBtn.value;
          hDecBtn.setAttribute("data-old", String(s.heater.cur));
          hDecBtn.disabled = !!s.heater.dec_off || !!s.heater.off;
        }
        var hIncBtn = heaterForm.querySelector('[data-dir="1"]');
        if (hIncBtn) {
          hIncBtn.value = s.heater.inc || hIncBtn.value;
          hIncBtn.setAttribute("data-old", String(s.heater.cur));
          hIncBtn.disabled = !!s.heater.inc_off || !!s.heater.off;
        }
      }
    }
    var banner = document.getElementById("status-banner");
    if (banner) {
      banner.textContent = s.banner || "";
      banner.hidden = !s.banner;
    }
  }

  function scrubbing() {
    return !!document.querySelector('.chart-wrap[data-scrub="1"], .chart-wrap[data-i], .zoom-wrap[data-scrub="1"], .zoom-wrap[data-i]');
  }

  // ---- Optional ChatGPT analysis (General + Per-Metric inside Zoom) ----
  function statusPillInfo(status) {
    if (status === "concern") return { text: "Concern", cls: "pill pill-danger" };
    if (status === "attention") return { text: "Attention", cls: "pill pill-warn" };
    return { text: "Good", cls: "pill pill-ok" };
  }

  function renderReport(target, statusEl, metaEl, rep) {
    if (!target || !rep) return;
    if (statusEl) {
      var st = statusPillInfo(rep.status);
      statusEl.textContent = st.text;
      statusEl.className = st.cls;
      statusEl.hidden = false;
    }
    if (metaEl) {
      var when = "";
      if (rep.generated_at) {
        var d = new Date(rep.generated_at);
        if (!isNaN(d.getTime())) when = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
      }
      metaEl.textContent = [rep.model || "", when].filter(Boolean).join(" · ");
    }
    target.textContent = "";
    if (rep.headline) {
      var h = document.createElement("p");
      h.className = "ai-headline";
      h.textContent = rep.headline;
      target.appendChild(h);
    }
    if (rep.summary) {
      var p = document.createElement("p");
      p.className = "ai-summary";
      p.textContent = rep.summary;
      target.appendChild(p);
    }
    if (Array.isArray(rep.metrics) && rep.metrics.length) {
      var mWrap = document.createElement("div");
      var ml = document.createElement("p");
      ml.className = "ai-section-label";
      ml.textContent = "By metric";
      mWrap.appendChild(ml);
      var grid = document.createElement("div");
      grid.className = "ai-metrics-grid";
      rep.metrics.forEach(function (m) {
        var card = document.createElement("div");
        card.className = "ai-metric-item";
        var head = document.createElement("div");
        head.className = "ai-metric-head";
        var name = document.createElement("span");
        name.textContent = m.label || m.id || "";
        var pill = document.createElement("span");
        var info = statusPillInfo(m.status);
        pill.className = info.cls;
        pill.textContent = info.text;
        head.appendChild(name);
        head.appendChild(pill);
        var desc = document.createElement("p");
        desc.className = "muted";
        desc.textContent = m.assessment || "";
        card.appendChild(head);
        card.appendChild(desc);
        grid.appendChild(card);
      });
      mWrap.appendChild(grid);
      target.appendChild(mWrap);
    }
    if (Array.isArray(rep.findings) && rep.findings.length) {
      var fWrap = document.createElement("div");
      var fl = document.createElement("p");
      fl.className = "ai-section-label";
      fl.textContent = "Findings";
      fWrap.appendChild(fl);
      var ful = document.createElement("ul");
      ful.className = "ai-list";
      rep.findings.forEach(function (item) {
        var li = document.createElement("li");
        li.textContent = item;
        ful.appendChild(li);
      });
      fWrap.appendChild(ful);
      target.appendChild(fWrap);
    }
    if (Array.isArray(rep.recommendations) && rep.recommendations.length) {
      var rWrap = document.createElement("div");
      var rl = document.createElement("p");
      rl.className = "ai-section-label";
      rl.textContent = "Recommendations";
      rWrap.appendChild(rl);
      var rul = document.createElement("ul");
      rul.className = "ai-list";
      rep.recommendations.forEach(function (item) {
        var li = document.createElement("li");
        li.textContent = item;
        rul.appendChild(li);
      });
      rWrap.appendChild(rul);
      target.appendChild(rWrap);
    }
  }

  function postAnalyze(scope, metricId, force, reqSpan) {
    var body = new URLSearchParams();
    body.set("token", token);
    body.set("scope", scope);
    if (metricId) body.set("metric", metricId);
    body.set("span", reqSpan || span);
    if (force) body.set("force", "1");
    return fetch("/api/ai/analyze", {
      method: "POST",
      headers: { "Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded" },
      body: body
    }).then(function (res) {
      return res.text().then(function (text) {
        var payload = {};
        try { payload = JSON.parse(text); } catch (e) { payload = { error: text || "Analysis failed" }; }
        if (!res.ok) throw new Error((payload && payload.error) || "Analysis failed");
        return payload.report;
      });
    });
  }

  function fetchCachedReport(scope, metricId, reqSpan) {
    var q = new URLSearchParams();
    q.set("scope", scope);
    if (metricId) q.set("metric", metricId);
    q.set("span", reqSpan || span);
    return fetch("/api/ai/report?" + q.toString(), { headers: { "Accept": "application/json" } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .catch(function () { return null; });
  }

  var dailyText = document.getElementById("ai-daily-text");

  function syncDailyFromCache() {
    if (!dailyText) return;
    fetchCachedReport("daily", "", "24h").then(function (res) {
      if (!res || !res.cached || !res.report) return;
      var text = (res.report.summary || res.report.headline || "").trim();
      dailyText.textContent = text;
      dailyText.hidden = !text;
    });
  }

  if (dailyText && !dailyText.textContent.trim()) {
    syncDailyFromCache();
  }
  if (dailyText) {
    document.addEventListener("visibilitychange", function () {
      if (!document.hidden) syncDailyFromCache();
    });
  }

  var zoomAIState = document.getElementById("zoom-ai-state");
  var zoomAIBody = document.getElementById("zoom-ai-body");
  var zoomAIStatus = document.getElementById("zoom-ai-status");
  var zoomAIMeta = document.getElementById("zoom-ai-meta");
  var zoomAIBtn = document.getElementById("zoom-ai-btn");
  var zoomAIRefresh = document.getElementById("zoom-ai-refresh");
  var zoomMetric = "";
  var zoomMetricTitle = "";

  function runMetricAI(force) {
    if (!zoomMetric || !zoomAIBtn) return;
    zoomAIBtn.disabled = true;
    if (zoomAIRefresh) zoomAIRefresh.disabled = true;
    if (zoomAIState) {
      zoomAIState.textContent = "Analyzing " + (zoomMetricTitle || zoomMetric) + " with ChatGPT…";
      zoomAIState.hidden = false;
    }
    var targetMetric = zoomMetric;
    postAnalyze("metric", targetMetric, force).then(function (rep) {
      if (targetMetric !== zoomMetric) return;
      if (zoomAIState) zoomAIState.hidden = true;
      renderReport(zoomAIBody, zoomAIStatus, zoomAIMeta, rep);
      if (zoomAIBtn) zoomAIBtn.hidden = true;
      if (zoomAIRefresh) zoomAIRefresh.hidden = false;
    }).catch(function (err) {
      if (targetMetric !== zoomMetric) return;
      if (zoomAIState) {
        zoomAIState.textContent = err.message;
        zoomAIState.hidden = false;
      }
    }).finally(function () {
      if (zoomAIBtn) zoomAIBtn.disabled = false;
      if (zoomAIRefresh) zoomAIRefresh.disabled = false;
    });
  }

  if (zoomAIBtn) zoomAIBtn.addEventListener("click", function () { runMetricAI(false); });
  if (zoomAIRefresh) zoomAIRefresh.addEventListener("click", function () { runMetricAI(true); });

  window.addEventListener("dmd-zoom-open", function (ev) {
    var detail = ev.detail || {};
    zoomMetric = detail.id || "";
    zoomMetricTitle = detail.title || zoomMetric;
    if (zoomAIBody) zoomAIBody.textContent = "";
    if (zoomAIStatus) zoomAIStatus.hidden = true;
    if (zoomAIMeta) zoomAIMeta.textContent = "";
    if (zoomAIState) { zoomAIState.textContent = ""; zoomAIState.hidden = true; }
    if (zoomAIBtn) {
      zoomAIBtn.hidden = false;
      zoomAIBtn.disabled = false;
      zoomAIBtn.textContent = "Analyze " + zoomMetricTitle;
    }
    if (zoomAIRefresh) zoomAIRefresh.hidden = true;
    if (!zoomMetric || !zoomAIBtn) return;
    var reqMetric = zoomMetric;
    fetchCachedReport("metric", reqMetric).then(function (res) {
      if (reqMetric !== zoomMetric) return;
      if (res && res.cached && res.report) {
        renderReport(zoomAIBody, zoomAIStatus, zoomAIMeta, res.report);
        if (zoomAIBtn) zoomAIBtn.hidden = true;
        if (zoomAIRefresh) zoomAIRefresh.hidden = false;
      }
    });
  });

  function fetchState() {
    var q = apiQuery();
    fetch("/api/state?" + q, { headers: { "Accept": "application/json" } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (payload) { applyState(payload); })
      .catch(function () {});
  }

  if (window.EventSource && document.querySelector(".topbar")) {
    var es = new EventSource("/events");
    var downAt = 0;
    es.onopen = function () {
      downAt = 0;
      if (live) live.hidden = true;
      syncDailyFromCache();
    };
    es.onerror = function () {
      if (!downAt) downAt = Date.now();
      if (live && Date.now() - downAt > 15000) live.hidden = false;
    };
    es.addEventListener("ai", function () {
      syncDailyFromCache();
    });
    es.addEventListener("state", function () {
      fetchState();
      if (!document.querySelector(".chart-card")) return;
      if (scrubbing()) return;
      if (span !== "6h" && span !== "24h") return;
      var q = apiQuery();
      fetch("/api/history?" + q, { headers: { "Accept": "application/json" } })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (payload) {
          if (payload) window.dispatchEvent(new CustomEvent("dmd-history", { detail: payload }));
        })
        .catch(function () {});
    });
  }
})();
