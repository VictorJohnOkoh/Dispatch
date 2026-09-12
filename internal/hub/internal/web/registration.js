const registrationForm = document.getElementById("host-registration");
if (registrationForm) {
  const result = document.getElementById("registration-result");
  registrationForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = registrationForm.querySelector("button");
    if (button.disabled) return;
    const input = {
      id: registrationForm.elements.id.value.trim(),
      address: registrationForm.elements.address.value.trim(),
      code: registrationForm.elements.code.value.trim(),
    };
    registrationForm.elements.code.value = "";
    button.disabled = true;
    result.textContent = "Checking SSH trust, key access and the Daemon. This can take up to 90 seconds.";
    try {
      const response = await fetch("/registration", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
        cache: "no-store",
      });
      input.code = "";
      if (!response.ok) {
        result.textContent = await response.text();
        return;
      }
      result.textContent = "Host registered.";
      window.location.reload();
    } catch {
      result.textContent = "The Hub reply was lost. Check the Hosts list before trying again. The Hub may have saved registration for recovery.";
    } finally {
      input.code = "";
      button.disabled = false;
    }
  });
  window.addEventListener("pagehide", () => { registrationForm.elements.code.value = ""; });
}
