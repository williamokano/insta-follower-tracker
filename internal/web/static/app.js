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
  function membershipPanel(uploadID, total, wide) {
    const div = document.createElement("div");
    div.className = "bucket members" + (wide ? " wide" : "");

    const h3 = document.createElement("h3");
    h3.textContent = "Followers at this point";
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

    fetch("/api/uploads/" + uploadID + "/followers", { headers: { Accept: "application/json" } })
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

  async function loadDetail(uploadID, container) {
    try {
      const response = await fetch("/api/uploads/" + uploadID + "/changes", {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) {
        throw new Error("request failed with status " + response.status);
      }
      const data = await response.json();

      container.textContent = "";
      if (data.is_baseline) {
        const note = document.createElement("p");
        note.className = "blurb baseline-note";
        note.textContent =
          "This is the earliest execution for the account, so there is nothing before it to " +
          "compare against. Here is the list it recorded.";
        container.appendChild(note);
      } else {
        container.appendChild(bucket("Followed", "gain", data.followed, "Nobody new."));
        container.appendChild(bucket("Unfollowed", "loss", data.unfollowed, "Nobody left."));
      }
      // On a baseline the list is the whole panel, so let it use the full width
      // rather than sit in one column of a grid sized for the delta buckets.
      container.appendChild(
        membershipPanel(uploadID, data.upload.follower_count, data.is_baseline === true)
      );
      container.dataset.loaded = "true";
    } catch (err) {
      container.textContent = "Could not load the details: " + err.message;
      container.dataset.loaded = "false";
    }
  }

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
    return root.querySelector(
      'tr.execution[data-status="pending"], tr.execution[data-status="processing"]'
    ) !== null;
  }

  // Processing happens after the upload response, so the table has to catch up
  // on its own rather than making the user reload.
  async function refresh() {
    if (!board || !hasWorkInFlight(board)) return;

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
