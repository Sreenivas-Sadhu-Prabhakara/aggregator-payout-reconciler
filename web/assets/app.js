/* Open MSME payout reconciler - the local interface.
   All arithmetic lives in the binary; this file only collects input, renders
   what comes back, and never talks to anything but this tool's own origin. */
(function () {
  "use strict";

  var MARKER = "X-Payout-Reconciler";
  var RUPEE = "₹";
  var state = null;

  /* ---- theme toggle (same behaviour as the shared kit) ---- */
  var root = document.documentElement;
  function storedTheme() {
    try { return localStorage.getItem("omsme-theme"); } catch (e) { return null; }
  }
  function currentTheme() {
    var t = root.getAttribute("data-theme");
    if (t) return t;
    return (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) ? "dark" : "light";
  }
  function paintThemer(b) {
    if (!b) return;
    var dark = currentTheme() === "dark";
    b.textContent = dark ? "☀ Light" : "☾ Dark";
    b.setAttribute("aria-label", dark ? "Switch to light theme" : "Switch to dark theme");
  }
  var themer = document.querySelector("[data-themer]");
  if (themer) {
    paintThemer(themer);
    themer.addEventListener("click", function () {
      var next = currentTheme() === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("omsme-theme", next); } catch (e) {}
      paintThemer(themer);
    });
    if (window.matchMedia) {
      window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
        if (!storedTheme()) paintThemer(themer);
      });
    }
  }

  /* ---- formatting ---- */

  /* money renders paise with Indian digit grouping. */
  function money(paise) {
    var neg = paise < 0;
    var p = Math.abs(Math.round(paise));
    var whole = String(Math.floor(p / 100));
    var frac = String(p % 100);
    if (frac.length < 2) frac = "0" + frac;
    var grouped;
    if (whole.length <= 3) {
      grouped = whole;
    } else {
      var head = whole.slice(0, whole.length - 3);
      var tail = whole.slice(whole.length - 3);
      var parts = [];
      while (head.length > 2) {
        parts.unshift(head.slice(head.length - 2));
        head = head.slice(0, head.length - 2);
      }
      if (head) parts.unshift(head);
      grouped = parts.join(",") + "," + tail;
    }
    return (neg ? "-" : "") + RUPEE + grouped + "." + frac;
  }

  function pct(bp) {
    var neg = bp < 0;
    var v = Math.abs(bp);
    var frac = String(v % 100);
    if (frac.length < 2) frac = "0" + frac;
    return (neg ? "-" : "") + Math.floor(v / 100) + "." + frac + "%";
  }

  function el(tag, text, cls) {
    var n = document.createElement(tag);
    if (text !== undefined && text !== null) n.textContent = String(text);
    if (cls) n.className = cls;
    return n;
  }

  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function say(id, msg, isError) {
    var n = document.getElementById(id);
    if (!msg) { n.hidden = true; n.textContent = ""; return; }
    n.hidden = false;
    n.textContent = (isError ? "Could not do that: " : "") + msg;
  }

  /* ---- talking to the binary ---- */

  function get(url) {
    return fetch(url, { headers: { "Accept": "application/json" } }).then(readJSON);
  }

  function post(url, body, extra) {
    var headers = { "Accept": "application/json" };
    headers[MARKER] = "1";
    if (extra) {
      for (var k in extra) { if (Object.prototype.hasOwnProperty.call(extra, k)) headers[k] = extra[k]; }
    }
    if (body !== undefined && typeof body !== "string") {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(body);
    } else if (typeof body === "string") {
      headers["Content-Type"] = "text/csv; charset=utf-8";
    }
    return fetch(url, { method: "POST", headers: headers, body: body }).then(readJSON);
  }

  function readJSON(res) {
    return res.json().then(function (data) {
      if (!res.ok) throw new Error((data && data.error) || ("the tool returned status " + res.status));
      return data;
    }, function () {
      throw new Error("the tool returned an unreadable response (status " + res.status + ")");
    });
  }

  /* ---- rendering ---- */

  function render(s) {
    state = s;
    var set = s.settings;

    document.getElementById("aggregator").value = set.aggregator || "";
    document.getElementById("commissionPct").value = set.commissionPct;
    document.getElementById("gstTcsPct").value = set.gstTcsPct;
    document.getElementById("tds194oPct").value = set.tds194oPct;
    document.getElementById("pgFeePct").value = set.pgFeePct;
    document.getElementById("toleranceRupees").value = set.toleranceRupees;
    seg("segPackaging", set.packagingBorneByRestaurant ? "yes" : "no");
    seg("segAudit", set.auditPgFee ? "yes" : "no");

    renderPresets(s);
    renderFiles(s);
    renderSummary(s);
    renderExports(s);
    renderDishes(s);
    renderProblems(s);
    renderAssumptions(s);

    document.getElementById("dataFile").textContent = "Everything is kept in " + s.dataFile;
  }

  function renderPresets(s) {
    var sel = document.getElementById("preset");
    if (sel.options.length === 0) {
      sel.appendChild(el("option", "Custom rates", ""));
      s.presets.forEach(function (p) {
        var o = el("option", p.name);
        o.value = p.name;
        sel.appendChild(o);
      });
      sel.addEventListener("change", function () {
        var chosen = null;
        s.presets.forEach(function (p) { if (p.name === sel.value) chosen = p; });
        if (!chosen) return;
        document.getElementById("gstTcsPct").value = chosen.gstTcsPct;
        document.getElementById("tds194oPct").value = chosen.tds194oPct;
        seg("segPackaging", chosen.packagingBorneByRestaurant ? "yes" : "no");
      });
    }
    sel.value = s.settings.rateSetName || "";
  }

  function renderFiles(s) {
    var f = s.files || {};
    describe("ordersNote", f.orders, s.orders, "orders");
    describe("settlementsNote", f.settlements, s.settlements, "settlement lines");
    var c = f.costs;
    document.getElementById("costsNote").textContent = c
      ? c.name + ": " + c.kept + " dish costs read on " + c.at
      : "No food costs loaded, so the per-dish table shows margins against a zero cost.";
  }

  function describe(id, note, kept, unit) {
    var n = document.getElementById(id);
    if (!note) { n.textContent = "Nothing imported yet."; return; }
    var txt = note.name + ": " + note.rows + " rows read, " + kept + " " + unit + " kept, on " + note.at + ".";
    if (note.recognised && note.recognised.length) {
      txt += " Columns recognised: " + note.recognised.join(", ") + ".";
    }
    if (note.note) txt += " " + note.note.charAt(0).toUpperCase() + note.note.slice(1) + ".";
    n.textContent = txt;
  }

  function renderSummary(s) {
    var t = s.report.totals;
    document.getElementById("owed").textContent = money(t.moneyOwedBack);

    var sub;
    if (!s.orders || !s.settlements) {
      sub = "Import both files to reconcile.";
    } else if (t.moneyOwedBack > 0) {
      sub = money(t.totalShortPaid) + " short-paid across " + t.shortPaidCount +
        " of " + t.matched + " settled orders, plus " + money(t.inBooksNotSettledValue) +
        " for " + t.inBooksNotSettledCount + " orders never settled at all.";
    } else {
      sub = "Every settled order reconciled within " + money(s.settings.toleranceRupees * 100) + " of the expected payout.";
    }
    document.getElementById("owedSub").textContent = sub;

    var rows = [
      ["Settled correctly", t.okCount, 0],
      ["Short-paid", t.shortPaidCount, -t.totalShortPaid],
      ["Over-paid", t.overPaidCount, t.totalOverPaid],
      ["Commission overcharged", t.overchargeCount, -t.totalCommissionOvercharge],
      ["In books, never settled", t.inBooksNotSettledCount, -t.inBooksNotSettledValue],
      ["Settled, not in books", t.settledNotInBooksCount, t.settledNotInBooksValue]
    ];
    var body = document.querySelector("#summary tbody");
    clear(body);
    rows.forEach(function (r) {
      var tr = document.createElement("tr");
      tr.appendChild(el("td", r[0]));
      tr.appendChild(el("td", r[1], "num"));
      var amount = el("td", r[2] === 0 ? "—" : money(r[2]), r[2] < 0 ? "num neg" : "num");
      tr.appendChild(amount);
      body.appendChild(tr);
    });
    var total = document.createElement("tr");
    total.className = "total";
    total.appendChild(el("td", "Money owed back"));
    total.appendChild(el("td", t.shortPaidCount + t.inBooksNotSettledCount, "num"));
    total.appendChild(el("td", money(t.moneyOwedBack), "num"));
    body.appendChild(total);

    var notes = [];
    if (t.overchargeCount > 0) {
      notes.push("Excess commission of " + money(t.totalCommissionOvercharge) +
        " is already inside the short payments and is not claimed twice.");
    }
    if (t.multiLineOrders > 0) {
      notes.push(t.multiLineOrders + " order(s) were settled over more than one statement row; those rows were summed.");
    }
    if (t.duplicateOrderIds > 0) {
      notes.push(t.duplicateOrderIds + " order reference(s) appear more than once in your books; only the first was reconciled.");
    }
    if (s.settings.commissionPct === 0 && s.orders > 0) {
      notes.push("The agreed commission is still 0%, so these figures assume the platform takes nothing. Enter the rate from your contract.");
    }
    var n = document.getElementById("integrityNote");
    n.textContent = notes.join(" ");
    n.hidden = notes.length === 0;
  }

  function renderExports(s) {
    var box = document.getElementById("exports");
    clear(box);
    s.exports.forEach(function (e) {
      var a = el("a", e.title, e.kind === "dispute-list" ? "btn primary" : "btn ghost");
      a.href = "api/export?kind=" + encodeURIComponent(e.kind);
      a.setAttribute("download", e.kind + ".csv");
      box.appendChild(a);
    });
  }

  function renderDishes(s) {
    var dishes = s.report.dishes || [];
    var body = document.querySelector("#dishes tbody");
    clear(body);
    var trap = 0;
    dishes.forEach(function (d) {
      if (d.flattersToDeceive) trap++;
      var tr = document.createElement("tr");

      var name = document.createElement("td");
      name.appendChild(el("div", d.name));
      var verdict;
      if (!d.costKnown) {
        verdict = "no food cost entered, so this is an upper bound";
      } else if (d.flattersToDeceive) {
        verdict = pct(d.grossMarginPct) + " on the menu card, " + money(d.netMargin) + " a plate after commission";
      } else if (d.lossMaking) {
        verdict = "loss-making before commission too";
      } else {
        verdict = pct(d.grossMarginPct) + " gross, " + pct(d.netMarginPct) + " after commission";
      }
      name.appendChild(el("small", verdict));
      tr.appendChild(name);

      tr.appendChild(el("td", d.units, "num"));
      tr.appendChild(el("td", money(d.netMargin), d.netMargin < 0 ? "num neg" : "num"));
      tr.appendChild(el("td", money(d.totalNetMargin), d.totalNetMargin < 0 ? "num neg" : "num"));
      body.appendChild(tr);
    });

    var note = document.getElementById("dishNote");
    if (dishes.length === 0) {
      note.textContent = "No dish lines were found in the order export, so there is nothing to cost. An export with item name, quantity and unit price columns turns this table on.";
    } else if (trap > 0) {
      note.textContent = "Sorted worst-first by the money each dish lost over the period. " + trap +
        " dish(es) show a positive margin on the menu card and lose money on every order once commission and packaging come out.";
    } else {
      note.textContent = "Sorted worst-first by the money each dish lost over the period.";
    }
  }

  function renderProblems(s) {
    var probs = s.report.problems || [];
    var body = document.querySelector("#problems tbody");
    clear(body);
    probs.forEach(function (p) {
      var tr = document.createElement("tr");
      tr.appendChild(el("td", p.source + ", line " + p.line + (p.column ? ", " + p.column : "")));
      tr.appendChild(el("td", p.value ? p.reason + " (" + p.value + ")" : p.reason));
      body.appendChild(tr);
    });
    document.getElementById("problemsNote").textContent = probs.length === 0
      ? "Every row in both files was read."
      : probs.length + " row(s) could not be read in full. Nothing is dropped quietly; fix these and import again, or download them as a CSV below.";
  }

  function renderAssumptions(s) {
    var c = s.constants;
    var items = [
      "GST TCS under section 52 of the CGST Act, 2017: the statute caps it at " + c.gstTcsCeiling +
        " of the net value of taxable supplies. " + c.gstTcsCeiling + " was the notified combined rate until 9 July 2024; " +
        c.gstTcsNotified + " combined has been notified since 10 July 2024.",
      "Restaurant service supplied through a platform was notified under section 9(5) with effect from 1 January 2022, and the platform pays that GST itself. CBIC has stated that TCS under section 52 is then not collected on it, which is why the restaurant rate set expects no TCS. TCS still applies to anything you sell through the platform that section 9(5) does not cover.",
      "TDS under section 194-O of the Income-tax Act, 1961: " + c.tds194o +
        " of the gross amount of sales with effect from 1 October 2024, down from " + c.tds194oOld +
        ". The lower rate assumes the platform holds your PAN.",
      "For the Philippines this tool asserts no withholding rate, because the regime differs and the figure is not one to guess. Commission, gateway fee, packaging and discounts are still reconciled in full; enter a rate yourself if one applies to you.",
      "Every rate above is a default you can overwrite in the form, and each is a single named constant in the code. Confirm the figures in force for your period with the authority or your accountant.",
      "The commissionable base is gross value less the restaurant-funded discount. Packaging, taxes and the delivery fee are outside it. Both withholding rates are applied to that same base, which is a simplification: the two statutes do not define their bases identically.",
      "Current terms: commission " + pct(Math.round(s.settings.commissionPct * 100)) +
        ", GST TCS " + pct(Math.round(s.settings.gstTcsPct * 100)) +
        ", TDS " + pct(Math.round(s.settings.tds194oPct * 100)) +
        ", gateway fee " + pct(Math.round(s.settings.pgFeePct * 100)) +
        ", tolerance " + money(s.settings.toleranceRupees * 100) + " an order."
    ];
    var ul = document.getElementById("assumptions");
    clear(ul);
    items.forEach(function (t) { ul.appendChild(el("li", t)); });
  }

  /* ---- segmented controls ---- */

  function seg(id, value) {
    var box = document.getElementById(id);
    var buttons = box.querySelectorAll("button");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute("aria-pressed", buttons[i].getAttribute("data-v") === value ? "true" : "false");
    }
  }

  function segValue(id) {
    var on = document.getElementById(id).querySelector('button[aria-pressed="true"]');
    return on ? on.getAttribute("data-v") : "no";
  }

  ["segPackaging", "segAudit"].forEach(function (id) {
    document.getElementById(id).addEventListener("click", function (ev) {
      var b = ev.target.closest("button[data-v]");
      if (!b) return;
      seg(id, b.getAttribute("data-v"));
    });
  });

  /* ---- actions ---- */

  function num(id) {
    var v = parseFloat(document.getElementById(id).value);
    return isFinite(v) && v >= 0 ? v : 0;
  }

  document.getElementById("terms").addEventListener("submit", function (ev) {
    ev.preventDefault();
    say("termsMsg", "");
    post("api/settings", {
      aggregator: document.getElementById("aggregator").value,
      commissionPct: num("commissionPct"),
      rateSetName: document.getElementById("preset").value || "Custom rates",
      gstTcsPct: num("gstTcsPct"),
      tds194oPct: num("tds194oPct"),
      pgFeePct: num("pgFeePct"),
      packagingBorneByRestaurant: segValue("segPackaging") === "yes",
      toleranceRupees: num("toleranceRupees"),
      auditPgFee: segValue("segAudit") === "yes"
    }).then(function (s) {
      render(s);
      say("termsMsg", "Terms saved and the figures below recalculated.");
    }).catch(function (err) {
      say("termsMsg", err.message, true);
    });
  });

  function upload(inputId, url) {
    var input = document.getElementById(inputId);
    input.addEventListener("change", function () {
      var file = input.files && input.files[0];
      if (!file) return;
      say("importMsg", "");
      var reader = new FileReader();
      reader.onerror = function () { say("importMsg", "that file could not be opened", true); };
      reader.onload = function () {
        post(url, String(reader.result), { "X-File-Name": file.name })
          .then(function (s) {
            render(s);
            say("importMsg", "Imported " + file.name + ".");
          })
          .catch(function (err) {
            say("importMsg", err.message, true);
          });
      };
      reader.readAsText(file);
    });
  }

  upload("ordersFile", "api/import/orders");
  upload("settlementsFile", "api/import/settlements");
  upload("costsFile", "api/import/costs");

  document.getElementById("loadExample").addEventListener("click", function () {
    say("importMsg", "");
    post("api/example").then(function (s) {
      render(s);
      say("importMsg", "Loaded a worked example. The figures are illustrative, not real trading data.");
    }).catch(function (err) { say("importMsg", err.message, true); });
  });

  document.getElementById("clearAll").addEventListener("click", function () {
    if (!window.confirm("Remove every imported order, settlement line and food cost from the data file? Your terms are kept.")) return;
    say("importMsg", "");
    post("api/reset").then(function (s) {
      render(s);
      ["ordersFile", "settlementsFile", "costsFile"].forEach(function (id) {
        document.getElementById(id).value = "";
      });
      say("importMsg", "Cleared.");
    }).catch(function (err) { say("importMsg", err.message, true); });
  });

  get("api/state").then(render).catch(function (err) {
    say("importMsg", err.message, true);
  });
})();
