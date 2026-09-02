// The one element both test pages are built from. It is the small surface the
// Client actually touches and nothing else, so what a test drives is the real
// file rather than a copy of it.

class El {
  constructor(tag) {
    this.tag = tag;
    this.className = "";
    this.dataset = {};
    this.children = [];
    this.parent = null;
    this._text = "";
  }

  // textContent is this element's own text and its children's, in order, which is
  // what a browser answers and what a Delta counts.
  get textContent() {
    return this._text + this.children.map((c) => c.textContent).join("");
  }

  set textContent(v) {
    this.children = [];
    this._text = String(v);
  }

  append(...parts) {
    for (const p of parts) {
      if (typeof p === "string") this._text += p;
      else {
        p.parent = this;
        this.children.push(p);
      }
    }
  }

  before(el) {
    el.parent = this.parent;
    this.parent.children.splice(this.parent.children.indexOf(this), 0, el);
  }

  replaceChildren(...parts) {
    this.children = [];
    this._text = "";
    this.append(...parts);
  }

  remove() {
    if (!this.parent) return;
    this.parent.children.splice(this.parent.children.indexOf(this), 1);
    this.parent = null;
  }

  replaceWith(el) {
    el.parent = this.parent;
    this.parent.children[this.parent.children.indexOf(this)] = el;
  }

  // querySelector answers the one selector page.js asks for, which is a class.
  querySelector(selector) {
    const want = selector.replace(".", "");
    for (const c of this.children) {
      if (c.className === want) return c;
      const found = c.querySelector(selector);
      if (found) return found;
    }
    return null;
  }
}
