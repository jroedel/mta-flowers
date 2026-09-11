/*
 * The flowers widget, as embedded in a Squarespace page.
 *
 * It is one file with no dependencies and no build step, because it is served
 * to a page we do not control and loaded by guests on hotel wifi on their way
 * to Mass. Every byte here is one somebody waits for.
 *
 * The page provides two globals through Squarespace's header injection:
 *
 *   window.FLOWERS_API       the base URL of this server, with a trailing /
 *   window.FLOWERS_EVENT_ID  the event, for the operator's own reference
 *
 * Both names are last year's, deliberately: the setup guide already written
 * for them stays true and the only edit on the Squarespace side is two strings.
 */
(function () {
  "use strict";

  var api = (window.FLOWERS_API || "").replace(/\/+$/, "");
  var mount = document.getElementById("flowers-widget");

  if (!mount) return;
  if (!api) {
    mount.textContent = "The flowers widget is not configured yet.";
    return;
  }

  /*
   * The browser's own identifier, in localStorage rather than a cookie.
   *
   * A cookie set by the API's domain inside this page would be a third-party
   * cookie: blocked outright by Safari, restricted by Chrome. The "you already
   * signed up" memory would then work for some guests and not others with no
   * pattern anybody could explain. localStorage is first-party to this page
   * and has none of that.
   *
   * It identifies a browser to itself and guards nothing. In a private window
   * it may throw, and the widget still has to work -- so a failure here falls
   * back to a token that lasts as long as the page.
   */
  function browserId() {
    var key = "flowers-browser-id";
    try {
      var existing = window.localStorage.getItem(key);
      if (existing) return existing;
      var made = randomId();
      window.localStorage.setItem(key, made);
      return made;
    } catch (e) {
      if (!browserId.fallback) browserId.fallback = randomId();
      return browserId.fallback;
    }
  }

  function randomId() {
    if (window.crypto && window.crypto.randomUUID) return window.crypto.randomUUID();
    return "b-" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  }

  function request(path, options) {
    options = options || {};
    return fetch(api + path, {
      method: options.method || "GET",
      headers: {
        "Content-Type": "application/json",
        "X-Flower-Browser": browserId()
      },
      body: options.body ? JSON.stringify(options.body) : undefined
    }).then(function (res) {
      return res.json().then(function (data) {
        if (!res.ok && data && data.error) throw new Error(data.error);
        if (!res.ok) throw new Error("Something went wrong. Please try again.");
        return data;
      });
    });
  }

  /* Styles are inline and scoped to our own class names. A Squarespace theme
   * will happily restyle a bare <button>, and the widget has to look the same
   * whichever template the site is on this year. */
  var css =
    ".flw{font:inherit;color:inherit}" +
    ".flw-card{background:#8ba888;border-radius:14px;padding:20px}" +
    ".flw-head{font-weight:700;font-size:1.15em;margin:0 0 14px;color:#1c2b1c}" +
    ".flw-input{display:block;width:100%;box-sizing:border-box;padding:14px 16px;" +
    "font:inherit;font-size:1.05em;border:0;border-radius:10px;background:#fff;color:#222}" +
    ".flw-btn{display:inline-block;margin-top:12px;padding:13px 26px;font:inherit;" +
    "font-size:1.05em;font-weight:700;color:#fff;background:#2f8f5b;border:0;" +
    "border-radius:999px;cursor:pointer}" +
    ".flw-btn:hover{background:#27794d}" +
    ".flw-btn[disabled]{opacity:.6;cursor:default}" +
    ".flw-btn-quiet{background:transparent;color:#1c2b1c;text-decoration:underline;" +
    "padding:8px 0;font-weight:400;font-size:.95em;border-radius:0}" +
    ".flw-btn-quiet:hover{background:transparent;opacity:.75}" +
    ".flw-count{margin:14px 0 0;color:#1c2b1c;font-size:.98em}" +
    ".flw-msg{margin:12px 0 0;font-size:.98em;color:#1c2b1c}" +
    ".flw-err{margin:12px 0 0;font-size:.98em;color:#7a1c1c;font-weight:600}" +
    ".flw-thanks{margin:0;font-size:1.1em;font-weight:700;color:#1c2b1c}";

  var style = document.createElement("style");
  style.appendChild(document.createTextNode(css));
  document.head.appendChild(style);

  mount.className = "flw";

  var state = null;
  var busy = false;

  /* Everything is built with createElement and textContent rather than
   * innerHTML. A guest's name is echoed back to them here, and this is the one
   * place it could become markup. */
  function el(tag, className, text) {
    var n = document.createElement(tag);
    if (className) n.className = className;
    if (text != null) n.textContent = text;
    return n;
  }

  function render(error) {
    mount.textContent = "";

    if (!state) {
      mount.appendChild(el("p", "flw-msg", "Loading…"));
      return;
    }

    var card = el("div", "flw-card");

    if (state.committed) {
      card.appendChild(el("p", "flw-thanks", "🌷 Thank you, " + state.name + "!"));
      card.appendChild(el("p", "flw-msg", state.instructions || ""));

      var undo = el("button", "flw-btn flw-btn-quiet", "I can no longer bring flowers");
      undo.type = "button";
      undo.disabled = busy;
      undo.addEventListener("click", cancel);
      card.appendChild(undo);
    } else {
      card.appendChild(el("p", "flw-head", "💐 Bring flowers for " + state.date_line + "?"));

      var input = el("input", "flw-input");
      input.type = "text";
      input.placeholder = "Your name";
      input.autocomplete = "name";
      input.maxLength = 120;
      input.addEventListener("keydown", function (e) {
        if (e.key === "Enter") { e.preventDefault(); commit(input.value); }
      });
      card.appendChild(input);

      var btn = el("button", "flw-btn", "🌷 I'll bring flowers");
      btn.type = "button";
      btn.disabled = busy;
      btn.addEventListener("click", function () { commit(input.value); });
      card.appendChild(btn);
    }

    card.appendChild(el("p", "flw-count",
      "🌼 Total commitments: " + state.count +
      (state.goal ? " of " + state.goal : "")));

    if (error) card.appendChild(el("p", "flw-err", error));

    mount.appendChild(card);
  }

  function commit(name) {
    if (busy) return;
    if (!name || !name.trim()) { render("Please type a name before pressing the button."); return; }

    busy = true;
    render();

    request("/api/commit", { method: "POST", body: { name: name } })
      .then(function (data) { state = data; busy = false; render(); })
      .catch(function (err) { busy = false; render(err.message); });
  }

  function cancel() {
    if (busy) return;
    busy = true;
    render();

    request("/api/cancel", { method: "POST" })
      .then(function (data) { state = data; busy = false; render(); })
      .catch(function (err) { busy = false; render(err.message); });
  }

  render();
  request("/api/state")
    .then(function (data) { state = data; render(); })
    .catch(function () {
      mount.textContent = "";
      mount.appendChild(el("p", "flw-err",
        "The sign-up is not reachable right now. Please try again in a moment."));
    });
})();
