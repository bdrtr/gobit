// funnel.js is the analytics plugin's panel screen (ADR 0153, ADR 0155).
//
// It is the first screen a PLUGIN put in the admin panel, and it is the second
// of ADR 0030's shape after the review queue: the panel renders a shell, this
// fills it from /admin/v1 with the operator's own session.
//
// No framework and no build step, for the reason the panel's own client has
// none: what this does is fetch and render, and a dependency would cost more
// than the code it saves.
//
// It never sees a token. The session is an HttpOnly cookie the browser attaches
// on its own, which is why the call passes credentials: "same-origin" and sets
// no header of its own.
(function () {
  "use strict";

  var app = document.getElementById("plugin-page");
  if (!app) {
    return;
  }

  var api = app.getAttribute("data-api");

  function show(message) {
    app.textContent = "";
    var line = document.createElement("p");
    line.className = "notice";
    line.textContent = message;
    app.appendChild(line);
  }

  // The rate is computed HERE and not asked of the server, because within one
  // day it is an approximation: a cart opened on Monday and completed on Tuesday
  // is counted on two days. The endpoint publishes the counts and leaves the
  // division to whoever is willing to say what it means.
  function rate(part, whole) {
    if (!whole) {
      return "—";
    }

    return Math.round((part / whole) * 100) + "%";
  }

  function cell(row, text) {
    var td = document.createElement("td");
    td.textContent = text;
    row.appendChild(td);
  }

  function render(days) {
    app.textContent = "";

    if (!days.length) {
      show("No carts were opened in this window.");
      return;
    }

    var table = document.createElement("table");
    var head = document.createElement("tr");
    ["Day", "Region", "Carts", "Completed", "Orders", "Conversion"].forEach(function (label) {
      var th = document.createElement("th");
      th.textContent = label;
      head.appendChild(th);
    });
    table.appendChild(head);

    days.forEach(function (day) {
      var row = document.createElement("tr");
      cell(row, day.day);
      cell(row, day.region_id);
      cell(row, String(day.carts_created));
      cell(row, String(day.carts_completed));
      cell(row, String(day.orders_placed));
      cell(row, rate(day.carts_completed, day.carts_created));
      table.appendChild(row);
    });

    app.appendChild(table);
  }

  // A 401 means the cookie expired while the page was open. Reloading is the
  // honest answer: the panel's login page is server-rendered and the redirect
  // belongs to the browser, not to a screen trying to draw a form.
  fetch(api + "/analytics/funnel", { credentials: "same-origin" })
    .then(function (response) {
      if (response.status === 401) {
        window.location.reload();
        return Promise.reject(new Error("the session ended"));
      }
      if (!response.ok) {
        return Promise.reject(new Error("the funnel could not be read (" + response.status + ")"));
      }

      return response.json();
    })
    .then(function (body) {
      render(body.data || []);
    })
    .catch(function (err) {
      show(err.message);
    });
})();
