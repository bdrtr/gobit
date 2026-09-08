// reviews.js is the panel's review screen, and the first client of the admin
// API the panel is becoming (ADR 0030).
//
// It is written without a framework and without a build step for the reason the
// SMTP plugin writes its own MIME and the error reporters write their own
// bodies: the dependency would cost more than the code it saves. What this file
// does is fetch, render and post — three things the platform already has.
//
// It never sees a token. The session is an HttpOnly cookie the browser attaches
// on its own (ADR 0011, unchanged by ADR 0030), which is why every call below
// passes credentials: "same-origin" and none of them sets that header itself.
(function () {
  "use strict";

  var app = document.getElementById("reviews-app");
  if (!app) {
    return;
  }

  var api = app.getAttribute("data-api");
  var statusLine = document.getElementById("reviews-status");

  // The filter the operator is looking at. "" is the whole queue; the other
  // values are what the API's ?suggested= takes, "none" included.
  var suggested = "";

  function show(message) {
    statusLine.textContent = message;
    statusLine.hidden = false;
  }

  // call is the one place a request is made, so the credentials mode, the
  // error shape and the "the session ended" case are decided once.
  //
  // A 401 means the cookie expired while the page was open. Reloading is the
  // honest answer: the panel's login page is server-rendered and the redirect
  // belongs to the browser, not to a screen trying to draw a form.
  function call(path, options) {
    var opts = options || {};
    opts.credentials = "same-origin";
    opts.headers = opts.headers || {};

    return fetch(api + path, opts).then(function (response) {
      if (response.status === 401) {
        window.location.reload();
        return Promise.reject(new Error("the session ended"));
      }
      if (!response.ok) {
        return response.json().then(
          function (body) {
            var message = body && body.error && body.error.message;
            throw new Error(message || "the request failed");
          },
          function () {
            throw new Error("the request failed (" + response.status + ")");
          }
        );
      }
      return response.json();
    });
  }

  function text(value) {
    return document.createTextNode(value == null ? "" : String(value));
  }

  function element(tag, className, child) {
    var node = document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (child != null) {
      node.appendChild(typeof child === "string" ? text(child) : child);
    }
    return node;
  }

  // Every value from the API reaches the page through a TEXT NODE and never
  // through innerHTML. A review is written by a member of the public and the
  // panel runs inside an administrator's session; the server-rendered pages get
  // this from the template engine's escaping, and this one has to do it itself.
  function cell(value) {
    return element("td", null, text(value));
  }

  function proposalCell(review) {
    var box = element("td", "proposal");
    if (!review.suggestion) {
      box.appendChild(element("span", "muted", "—"));
      return box;
    }
    box.appendChild(element("strong", null, review.suggestion.status));
    box.appendChild(element("div", "reason", review.suggestion.reason));
    box.appendChild(element("div", "muted", review.suggestion.model));
    return box;
  }

  function decide(id, status, row) {
    var note = "";
    if (status === "rejected") {
      // The API refuses a rejection with no note, and asking here says why
      // rather than showing the refusal afterwards.
      note = window.prompt("Why is this review rejected?") || "";
      if (note.trim() === "") {
        return;
      }
    }

    call("/reviews/" + encodeURIComponent(id) + "/status", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status: status, note: note }),
    }).then(
      function () {
        row.parentNode.removeChild(row);
      },
      function (err) {
        show(err.message);
      }
    );
  }

  function actionCell(review, row) {
    var box = element("td", "actions");
    ["approved", "rejected"].forEach(function (status) {
      var button = element("button", null, status === "approved" ? "Approve" : "Reject");
      button.type = "button";
      button.addEventListener("click", function () {
        decide(review.id, status, row);
      });
      box.appendChild(button);
    });
    return box;
  }

  function rowFor(review) {
    var row = document.createElement("tr");
    row.appendChild(cell(review.product_id));
    row.appendChild(cell(review.rating));
    var body = element("td");
    if (review.title) {
      body.appendChild(element("strong", null, review.title));
    }
    body.appendChild(element("div", null, review.body));
    body.appendChild(element("div", "muted", review.author_name));
    row.appendChild(body);
    row.appendChild(proposalCell(review));
    row.appendChild(actionCell(review, row));
    return row;
  }

  function table(reviews) {
    var head = document.createElement("tr");
    ["Product", "Rating", "Review", "Model says", ""].forEach(function (label) {
      head.appendChild(element("th", null, label));
    });

    var body = document.createElement("tbody");
    reviews.forEach(function (review) {
      body.appendChild(rowFor(review));
    });

    var node = document.createElement("table");
    node.appendChild(head);
    node.appendChild(body);

    var scroll = element("div", "scroll");
    scroll.appendChild(node);
    return scroll;
  }

  function filters() {
    var bar = element("div", "filters");
    [
      ["", "Everything waiting"],
      ["rejected", "Model says reject"],
      ["approved", "Model says approve"],
      ["none", "Not looked at yet"],
    ].forEach(function (pair) {
      var button = element("button", pair[0] === suggested ? "current" : null, pair[1]);
      button.type = "button";
      button.addEventListener("click", function () {
        suggested = pair[0];
        load();
      });
      bar.appendChild(button);
    });
    return bar;
  }

  // The agreement report is asked for ONCE per load and shown as counts, never
  // as a percentage — which is the API's own decision (ADR 0074) and would be
  // undone by a screen that divided the two numbers.
  function agreement() {
    var box = element("div", "agreement");
    call("/reviews/suggestion-agreement", {}).then(
      function (page) {
        if (!page.data || page.data.length === 0) {
          return;
        }
        page.data.forEach(function (row) {
          box.appendChild(
            element(
              "div",
              "muted",
              row.model + ": agreed with the operator on " + row.agreed + " of " + row.decided + " decided"
            )
          );
        });
      },
      function () {
        // A failed report must not take the queue down with it: the queue is
        // the work and this is the footnote.
        box.appendChild(element("div", "muted", "the agreement report could not be read"));
      }
    );
    return box;
  }

  function load() {
    show("Loading…");

    var path = "/reviews?status=submitted&limit=25";
    if (suggested !== "") {
      path += "&suggested=" + encodeURIComponent(suggested);
    }

    call(path, {}).then(
      function (page) {
        while (app.firstChild) {
          app.removeChild(app.firstChild);
        }
        app.appendChild(statusLine);
        app.appendChild(filters());

        if (!page.data || page.data.length === 0) {
          show("Nothing is waiting.");
          return;
        }

        statusLine.hidden = true;
        app.appendChild(table(page.data));
        app.appendChild(agreement());
      },
      function (err) {
        show(err.message);
      }
    );
  }

  load();
})();
