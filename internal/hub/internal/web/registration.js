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

// Scanning is the same credential the human would paste, read from a camera or
// a photo. The browser's own barcode reader does the decoding, so the Hub ships
// no scanning library and still needs no internet.
const scan = document.getElementById("registration-scan");
if (scan && "BarcodeDetector" in window) {
  const detector = new BarcodeDetector({ formats: ["qr_code"] });
  const code = document.getElementById("registration-code");
  const video = document.getElementById("registration-scan-video");
  const start = document.getElementById("registration-scan-start");
  const stop = document.getElementById("registration-scan-stop");
  const file = document.getElementById("registration-scan-file");
  const result = document.getElementById("registration-scan-result");
  let stream = null;

  const close = () => {
    if (stream) stream.getTracks().forEach((track) => track.stop());
    stream = null;
    video.hidden = true;
    stop.hidden = true;
    start.hidden = false;
  };

  const accept = (codes) => {
    const found = codes.find((c) => c.rawValue.startsWith("dispatch"));
    if (!found) return false;
    code.value = found.rawValue.trim();
    result.textContent = "Code read. Check the Host ID, then register.";
    return true;
  };

  const read = async () => {
    while (stream) {
      try {
        if (accept(await detector.detect(video))) {
          close();
          return;
        }
      } catch {
        result.textContent = "The camera picture could not be read.";
        close();
        return;
      }
      await new Promise((done) => setTimeout(done, 200));
    }
  };

  start.addEventListener("click", async () => {
    result.textContent = "Hold the Host's QR code in front of the camera.";
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
    } catch {
      result.textContent = "The camera is not available. Use a photo, or paste the text code.";
      return;
    }
    video.srcObject = stream;
    video.hidden = false;
    start.hidden = true;
    stop.hidden = false;
    await video.play();
    read();
  });

  stop.addEventListener("click", () => {
    close();
    result.textContent = "";
  });

  file.addEventListener("change", async () => {
    const picture = file.files[0];
    file.value = "";
    if (!picture) return;
    try {
      const bitmap = await createImageBitmap(picture);
      const found = accept(await detector.detect(bitmap));
      bitmap.close();
      if (!found) result.textContent = "No Dispatch QR code was found in that picture.";
    } catch {
      result.textContent = "That picture could not be read.";
    }
  });

  window.addEventListener("pagehide", close);
  scan.hidden = false;
}
