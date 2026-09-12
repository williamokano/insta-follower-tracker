// Progressive enhancement for the dashboard. The page is fully rendered by the
// server; this only adds the collapsible per-execution detail and a refresh
// while uploads are still being processed in the background.

(function () {
  "use strict";

  const board = document.getElementById("executions");

  /** Render a list of usernames, or a placeholder when there are none. */
  function peopleList(entries, emptyLabel) {
    if (!entries || entries.length === 0) {
      const p = document.createElement("p");
      p.className = "empty";
      p.textContent = emptyLabel;
      return p;
    }

    const ul = document.createElement("ul");
    ul.className = "people";
    for (const entry of entries) {
      const li = document.createElement("li");
      const a = document.createElement("a");
      a.href = "https://www.instagram.com/" + encodeURIComponent(entry.username);
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.textContent = entry.username;
      li.appendChild(a);
      ul.appendChild(li);
    }
    return ul;
  }

  function bucket(title, className, entries, emptyLabel) {
    const div = document.createElement("div");
    div.className = "bucket " + className;

    const h3 = document.createElement("h3");
    h3.textContent = title;
    const count = document.createElement("span");
    count.className = "count";
    count.textContent = entries ? entries.length : 0;
    h3.appendChild(count);

    div.appendChild(h3);
    div.appendChild(peopleList(entries, emptyLabel));
    return div;
  }

  /**
   * The full list an execution recorded, with a filter. This is the whole
   * panel for a first upload, which has nothing to compare against, and sits
   * beside the deltas for every execution after it.
   */
  function membershipPanel(uploadID, kind, total, heading, wide) {
    const div = document.createElement("div");
    div.className = "bucket members" + (wide ? " wide" : "");

    const h3 = document.createElement("h3");
    h3.textContent = heading;
    const count = document.createElement("span");
    count.className = "count";
    count.textContent = total;
    h3.appendChild(count);
    div.appendChild(h3);

    const search = document.createElement("input");
    search.type = "search";
    search.className = "member-filter";
    search.placeholder = "Filter " + total + " " + (total === 1 ? "name" : "names");
    search.setAttribute("aria-label", "Filter followers");
    div.appendChild(search);

    const listHolder = document.createElement("div");
    listHolder.textContent = "Loading…";
    div.appendChild(listHolder);

    let loaded = [];
    const render = function () {
      const needle = search.value.trim().toLowerCase();
      const shown = needle === ""
        ? loaded
        : loaded.filter(function (f) { return f.username.indexOf(needle) !== -1; });

      listHolder.textContent = "";
      if (needle !== "") {
        const note = document.createElement("p");
        note.className = "blurb";
        note.textContent = shown.length + " of " + loaded.length + " match";
        listHolder.appendChild(note);
      }
      listHolder.appendChild(peopleList(shown, needle === "" ? "Nobody." : "No match."));
    };

    search.addEventListener("input", render);

    fetch("/api/uploads/" + uploadID + "/followers?list=" + encodeURIComponent(kind),
      { headers: { Accept: "application/json" } })
      .then(function (response) {
        if (!response.ok) throw new Error("status " + response.status);
        return response.json();
      })
      .then(function (data) {
        loaded = data.followers || [];
        render();
      })
      .catch(function (err) {
        listHolder.textContent = "Could not load the list: " + err.message;
      });

    return div;
  }

  /** Human wording for a list, used in the panels within it. */
  const LIST_WORDING = {
    followers: { joined: "Followed", left: "Unfollowed", here: "Followers at this point" },
    following: { joined: "Started following", left: "Stopped following", here: "Following at this point" },
  };

  function wordingFor(kind) {
    return LIST_WORDING[kind] || { joined: "Added", left: "Removed", here: "Members at this point" };
  }

  /** Load and render one list's panels into the container. */
  async function loadList(uploadID, kind, totals, container) {
    const words = wordingFor(kind);
    const query = "?list=" + encodeURIComponent(kind);

    const response = await fetch("/api/uploads/" + uploadID + "/changes" + query, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) throw new Error("request failed with status " + response.status);
    const data = await response.json();

    const panels = document.createElement("div");
    panels.className = "detail-panels";

    if (data.is_baseline) {
      const note = document.createElement("p");
      note.className = "blurb baseline-note";
      note.textContent =
        "This is the earliest execution for the account, so there is nothing before it to " +
        "compare against. Here is the list it recorded.";
      panels.appendChild(note);
    } else {
      panels.appendChild(bucket(words.joined, "gain", data.followed, "Nobody new."));
      panels.appendChild(bucket(words.left, "loss", data.unfollowed, "Nobody left."));
    }

    const total = totals[kind] === undefined ? 0 : totals[kind];
    panels.appendChild(membershipPanel(uploadID, kind, total, words.here, data.is_baseline === true));

    container.textContent = "";
    container.appendChild(panels);
  }

  /** Accounts you follow who do not follow you back, and the reverse. */
  async function loadRelationships(uploadID, container) {
    const response = await fetch("/api/uploads/" + uploadID + "/relationships", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) throw new Error("request failed with status " + response.status);
    const data = await response.json();

    const panels = document.createElement("div");
    panels.className = "detail-panels";

    if (data.message) {
      const note = document.createElement("p");
      note.className = "blurb baseline-note";
      note.textContent = data.message;
      panels.appendChild(note);
    } else {
      panels.appendChild(bucket("Not following you back", "loss", data.not_following_back,
        "Everybody you follow follows you back."));
      panels.appendChild(bucket("Fans", "gain", data.fans,
        "You follow back everybody who follows you."));
    }

    container.textContent = "";
    container.appendChild(panels);
  }

  async function loadDetail(uploadID, container) {
    try {
      const response = await fetch("/api/uploads/" + uploadID + "/followers", {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) {
        throw new Error("request failed with status " + response.status);
      }
      const summary = await response.json();

      // A refused export has no lists to show; what it has is a reason, and it
      // belongs here rather than wrapped down the status column of the table.
      if (summary.upload && summary.upload.status === "failed") {
        container.textContent = "";

        const panel = document.createElement("div");
        panel.className = "failure";

        const heading = document.createElement("h3");
        heading.textContent = "This export was not recorded";
        panel.appendChild(heading);

        const reason = document.createElement("p");
        panel.appendChild(reason);
        // Error strings are written lowercase and unpunctuated, as Go wants them.
        // Under a heading they read as a sentence, so present them as one.
        const message = summary.upload.error_message;
        if (message) {
          const text = message[0].toUpperCase() + message.slice(1);
          reason.textContent = /[.!?]$/.test(text) ? text : text + ".";
        } else {
          reason.textContent = "No reason was recorded.";
        }

        container.appendChild(panel);
        container.dataset.loaded = "true";
        return;
      }

      const totals = {};
      const kinds = [];
      for (const entry of summary.lists || []) {
        totals[entry.kind] = entry.member_count;
        kinds.push(entry.kind);
      }
      if (kinds.length === 0) kinds.push("followers");

      container.textContent = "";

      const body = document.createElement("div");
      const tabs = document.createElement("div");
      tabs.className = "list-tabs";

      const show = function (loader, button) {
        for (const other of tabs.querySelectorAll("button")) {
          other.setAttribute("aria-pressed", other === button ? "true" : "false");
        }
        body.textContent = "Loading…";
        loader(body).catch(function (err) {
          body.textContent = "Could not load: " + err.message;
        });
      };

      const addTab = function (label, loader) {
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = label;
        button.setAttribute("aria-pressed", "false");
        button.addEventListener("click", function () { show(loader, button); });
        tabs.appendChild(button);
        return button;
      };

      let firstTab = null;
      for (const kind of kinds) {
        const label = LIST_LABELS[kind] || kind;
        const button = addTab(label, function (target) {
          return loadList(uploadID, kind, totals, target);
        });
        if (firstTab === null) firstTab = button;
      }

      if (totals.following !== undefined && totals.followers !== undefined) {
        addTab("Not following back", function (target) {
          return loadRelationships(uploadID, target);
        });
      }

      // Only worth showing tabs when there is more than one thing to choose.
      if (tabs.children.length > 1) container.appendChild(tabs);
      container.appendChild(body);

      firstTab.click();
      container.dataset.loaded = "true";
    } catch (err) {
      container.textContent = "Could not load the details: " + err.message;
      container.dataset.loaded = "false";
    }
  }

  /** Labels for the list tabs, filled in from the server on first load. */
  const LIST_LABELS = {};

  fetch("/api/lists", { headers: { Accept: "application/json" } })
    .then(function (r) { return r.ok ? r.json() : { lists: [] }; })
    .then(function (data) {
      for (const info of data.lists || []) LIST_LABELS[info.kind] = info.label;
    })
    .catch(function () { /* labels fall back to the raw kind */ });

  function wireToggles(root) {
    for (const button of root.querySelectorAll("button.toggle")) {
      button.addEventListener("click", function () {
        const uploadID = button.dataset.upload;
        const row = root.querySelector('tr[data-detail="' + uploadID + '"]');
        if (!row) return;

        const expanded = button.getAttribute("aria-expanded") === "true";
        button.setAttribute("aria-expanded", expanded ? "false" : "true");
        row.hidden = expanded;

        const container = row.querySelector(".detail");
        if (!expanded && container.dataset.loaded !== "true") {
          loadDetail(uploadID, container);
        }
      });
    }
  }

  /** True while any execution is still queued or being processed. */
  function hasWorkInFlight(root) {
    if (root.querySelector(
      'tr.execution[data-status="pending"], tr.execution[data-status="processing"]'
    ) !== null) {
      return true;
    }
    // A reread leaves the execution completed throughout, so its progress is
    // not visible in the rows; ask the service instead.
    return rereadingInFlight;
  }

  let rereadingInFlight = false;

  function checkRereading() {
    return fetch("/healthz", { headers: { Accept: "application/json" } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) {
        rereadingInFlight = Boolean(data && data.rereading > 0);
      })
      .catch(function () { rereadingInFlight = false; });
  }

  // Processing happens after the upload response, so the table has to catch up
  // on its own rather than making the user reload.
  async function refresh() {
    if (!board) return;
    await checkRereading();
    if (!hasWorkInFlight(board)) return;

    try {
      const response = await fetch(window.location.href, {
        headers: { Accept: "text/html" },
      });
      if (!response.ok) return;

      const html = await response.text();
      const parsed = new DOMParser().parseFromString(html, "text/html");
      const fresh = parsed.getElementById("executions");
      if (!fresh) return;

      // Preserve whichever detail panels the user has already opened.
      const open = new Set(
        Array.from(board.querySelectorAll('button.toggle[aria-expanded="true"]')).map(
          (b) => b.dataset.upload
        )
      );

      board.innerHTML = fresh.innerHTML;
      wireToggles(board);

      for (const uploadID of open) {
        const button = board.querySelector('button.toggle[data-upload="' + uploadID + '"]');
        if (button) button.click();
      }
    } catch (err) {
      // A failed refresh is not worth interrupting the page for; the next tick
      // will try again.
    }
  }

  if (board) {
    wireToggles(board);
    setInterval(refresh, 3000);
  }

  // Instagram names a download instagram-<handle>-<date>-<hash>.zip, so the
  // account can be filled in the moment a file is chosen rather than typed.
  // The server reads it from the archive too, for files that were renamed; this
  // only saves the round trip and shows what will happen.
  const ARCHIVE_NAME = /^instagram-([a-z0-9._]+)-\d{4}-\d{2}-\d{2}-[a-z0-9]+$/i;

  const fileInput = document.getElementById("file");
  const accountInput = document.getElementById("account");
  const accountHint = document.getElementById("account-hint");

  if (fileInput && accountInput) {
    fileInput.addEventListener("change", function () {
      const file = fileInput.files && fileInput.files[0];
      if (!file) return;

      const match = ARCHIVE_NAME.exec(file.name.replace(/\.zip$/i, ""));
      if (!match) return;

      // Never overwrite a handle the person chose themselves.
      if (accountInput.value.trim() !== "" && accountInput.dataset.autofilled !== "true") {
        return;
      }

      accountInput.value = match[1].toLowerCase();
      accountInput.dataset.autofilled = "true";
      if (accountHint) {
        accountHint.textContent = "Read from the file name. Change it if that is wrong.";
      }
    });

    accountInput.addEventListener("input", function () {
      accountInput.dataset.autofilled = "false";
    });
  }

  // Give immediate feedback on submit, since the upload itself can take a moment
  // even though processing does not.
  const uploadForm = document.querySelector("form.upload");
  if (uploadForm) {
    uploadForm.addEventListener("submit", function () {
      const button = uploadForm.querySelector("button[type=submit]");
      if (button) {
        button.disabled = true;
        button.textContent = "Uploading…";
      }
    });
  }
})();
