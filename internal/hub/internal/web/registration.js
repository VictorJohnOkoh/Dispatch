const registrationForm = document.getElementById("host-registration");
if (registrationForm) {
  const result = document.getElementById("registration-result");
  const fields = registrationForm.elements;
  const panels = registrationForm.querySelectorAll(".registration-panel");
  const addressLabel = document.getElementById("registration-address-label");

  const userLabel = document.getElementById("registration-user-label");

  // Each way in needs its own fields, so the method decides which panels show and
  // which entries the browser insists on. The account panel is shared: the code
  // names an account, and the existing-login way has nothing to read one from.
  const shown = {
    code: ["code", "account"],
    existing: ["account", "existing"],
  };
  const needed = {
    code: ["code", "password"],
    existing: ["address", "user", "daemonPort"],
  };
  const every = ["code", "address", "user", "daemonPort", "password"];

  const chosen = () => fields.method.value;

  const show = () => {
    const method = chosen();
    panels.forEach((panel) => { panel.hidden = !shown[method].includes(panel.dataset.method); });
    every.forEach((name) => { fields[name].required = needed[method].includes(name); });
    addressLabel.textContent = method === "code"
      ? "SSH port or address (optional; the code carries one)"
      : "SSH address of the Host";
    userLabel.textContent = method === "code"
      ? "Account on the Host (optional; the code carries one)"
      : "Account on the Host";
  };

  const body = () => {
    const method = chosen();
    const shared = { id: fields.id.value.trim(), address: fields.address.value.trim(), user: fields.user.value.trim() };
    if (method === "code") return { ...shared, code: fields.code.value.trim(), password: fields.password.value };
    return { ...shared, method, daemonPort: Number(fields.daemonPort.value) };
  };

  const forget = () => { fields.code.value = ""; fields.password.value = ""; };

  registrationForm.querySelectorAll("input[name=method]").forEach((radio) => radio.addEventListener("change", show));
  show();

  registrationForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = registrationForm.querySelector("button[type=submit]");
    if (button.disabled) return;
    const input = body();
    forget();
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
      input.password = "";
      if (!response.ok) {
        result.textContent = await response.text();
        return;
      }
      result.textContent = "Host registered.";
      window.location.reload();
    } catch {
      result.textContent = "The Hub reply was lost. Check the Hosts list before trying again.";
    } finally {
      input.code = "";
      input.password = "";
      button.disabled = false;
    }
  });
  window.addEventListener("pagehide", forget);
}

// Scanning reads the same code the human would paste, read from a camera or
// a photo. The browser's own reader does it where there is one; everywhere else
// the Hub serves its own copy of jsQR. Either way nothing is fetched from the
// internet, so a Hub on a machine with no internet still scans.
const scan = document.getElementById("registration-scan");
if (scan) {
  const native = "BarcodeDetector" in window ? new BarcodeDetector({ formats: ["qr_code"] }) : null;
  const code = document.getElementById("registration-code");
  const video = document.getElementById("registration-scan-video");
  const start = document.getElementById("registration-scan-start");
  const stop = document.getElementById("registration-scan-stop");
  const photo = document.getElementById("registration-scan-photo");
  const file = document.getElementById("registration-scan-file");
  const result = document.getElementById("registration-scan-result");
  const canvas = document.createElement("canvas");
  const paint = canvas.getContext("2d", { willReadFrequently: true });
  let stream = null;
  let loading = null;

  // jsQR is a quarter of a megabyte, so the Hosts page does not carry it until
  // a human asks for a scan.
  const vendored = () => {
    if (!loading) {
      loading = new Promise((done, fail) => {
        const tag = document.createElement("script");
        tag.src = "/jsqr.js";
        tag.addEventListener("load", () => done(window.jsQR));
        tag.addEventListener("error", () => fail(new Error("the QR reader did not load")));
        document.head.append(tag);
      });
    }
    return loading;
  };

  // A telephone photograph is far larger than a QR reader needs, and a large
  // picture is slow to read, so the long edge is capped.
  const longEdge = 1600;

  const detect = async (source) => {
    if (native) return native.detect(source);
    const jsQR = await vendored();
    const width = source.videoWidth || source.width;
    const height = source.videoHeight || source.height;
    if (!width || !height) return [];
    const scale = Math.min(1, longEdge / Math.max(width, height));
    canvas.width = Math.round(width * scale);
    canvas.height = Math.round(height * scale);
    paint.drawImage(source, 0, 0, canvas.width, canvas.height);
    const found = jsQR(paint.getImageData(0, 0, canvas.width, canvas.height).data, canvas.width, canvas.height);
    return found ? [{ rawValue: found.data }] : [];
  };

  const say = (text, read) => {
    result.textContent = text;
    result.classList.toggle("read", read === true);
  };

  const close = () => {
    if (stream) stream.getTracks().forEach((track) => track.stop());
    stream = null;
    video.hidden = true;
    stop.hidden = true;
    start.hidden = false;
  };

  // A read fills the box the human would have pasted into, so the rest of
  // registration cannot tell the two apart.
  const accept = (codes) => {
    const found = codes.find((c) => c.rawValue.trim().startsWith("dispatch"));
    if (!found) return false;
    code.value = found.rawValue.trim();
    code.scrollIntoView({ block: "nearest" });
    say("Code read. Check the Host ID, then select Register Host.", true);
    return true;
  };

  const read = async () => {
    while (stream) {
      try {
        if (accept(await detect(video))) {
          close();
          return;
        }
      } catch (err) {
        say(err.message || "The camera picture could not be read.");
        close();
        return;
      }
      await new Promise((done) => setTimeout(done, 200));
    }
  };

  start.addEventListener("click", async () => {
    say("Starting the camera.");
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
    } catch {
      say("The camera is not available. Use a photo, or paste the text code.");
      return;
    }
    video.srcObject = stream;
    video.hidden = false;
    start.hidden = true;
    stop.hidden = false;
    await video.play();
    say("Hold the Host's QR code in front of the camera.");
    read();
  });

  stop.addEventListener("click", () => {
    close();
    say("");
  });

  photo.addEventListener("click", () => file.click());

  file.addEventListener("change", async () => {
    const picture = file.files[0];
    file.value = "";
    if (!picture) return;
    say("Reading the picture.");
    try {
      const bitmap = await createImageBitmap(picture);
      const found = accept(await detect(bitmap));
      bitmap.close();
      if (!found) say("No Dispatch QR code was found in that picture.");
    } catch (err) {
      say(err.message || "That picture could not be read.");
    }
  });

  window.addEventListener("pagehide", close);
  scan.hidden = false;
}
