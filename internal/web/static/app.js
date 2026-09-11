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
        const p = document.createElement("p");
        p.className = "empty";
        p.textContent =
          "This is the first execution for the account, so there is nothing before it to compare against.";
        container.appendChild(p);
      } else {
        container.appendChild(bucket("Followed", "gain", data.followed, "Nobody new."));
        container.appendChild(bucket("Unfollowed", "loss", data.unfollowed, "Nobody left."));
      }
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
