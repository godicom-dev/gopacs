for (const button of document.querySelectorAll("[data-delete]")) {
  button.addEventListener("click", async () => {
    if (!confirm("Delete this " + button.dataset.kind + " and everything under it?")) return;
    const res = await fetch(button.dataset.delete, { method: "DELETE" });
    if (!res.ok) {
      button.insertAdjacentText("afterend", " Delete failed");
      return;
    }
    location.href = button.dataset.next;
  });
}

const upload = document.querySelector("[data-upload]");
if (upload) {
  upload.addEventListener("click", async () => {
    const input = document.getElementById("files");
    const log = document.getElementById("log");
    log.replaceChildren();
    for (const file of input.files) {
      const item = document.createElement("li");
      item.className = "row";
      item.textContent = file.name + " …";
      log.append(item);
      try {
        const res = await fetch("/instances", {
          method: "POST",
          headers: { "Content-Type": "application/dicom" },
          body: file
        });
        const body = await res.json();
        if (!res.ok) throw new Error(body.Message || res.statusText);
        item.textContent = file.name + " — " + body.Status;
      } catch (err) {
        item.textContent = file.name + " — " + err.message;
        item.classList.add("error");
      }
    }
  });
}
