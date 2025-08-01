export class Extractor {
  constructor(debug) {
    this.logs = [];
    this.debug = debug;
    this.punctuationRe = /[.]\s|[,!?;]/g;
    this.weights = {
      textArea: 2,
      textLen: 5,
      pCount: 10,
      bpCount: -5,
      longCount: 5,
      actionTextLen: -5,
      depth: 2,
      tagScore: 2,
    }
  }

  log(...vs) {
    if (vs.length != 1) throw new Error(`bad log call: ${JSON.stringify(vs)}`)
    this.logs.push(vs[0])
  }

  run(document) {
    if (document.readyState !== "complete") throw new Error("document not ready")
    document.querySelectorAll("img").forEach(img => this.inlineIMG(img));
    document.querySelectorAll("svg").forEach(img => this.inlineSVG(img));
    const meta = this.parseMeta(document);
    document.querySelectorAll("style").forEach(el => el.remove())
    const candidates = this.candidates(document);
    if (this.debug) console.log(candidates.length)
    for (let el of candidates.slice(0,5)) {
      this.log({tag: el.tagName, id: el.id, class: el.className, scores: el.scores, _: el._})
      if (this.debug) console.log(el, el.scores, el._)
    }

    return {
      ...meta,
      url: location.href,
      scores: candidates[0].scores,
      html: candidates[0].outerHTML,
      logs: this.logs,
    };
  }

  candidates(document) {
    this.max = {};
    const windowArea = window.innerHeight * window.innerWidth;
    const actionSelector = "a, button";
    // calculate visible text metrics (length, area, count of punctuation chars) for all elements
    const pCountByFont = {};
    const textWalker = document.createTreeWalker(document, NodeFilter.SHOW_TEXT);
    while (textWalker.nextNode()) {
      const n = textWalker.currentNode, el = n.parentElement;
      const txt = n.textContent.trim();
      if (!txt || !this.isVisible(el)) continue;
      const style = getComputedStyle(el),
            pCount = txt.match(this.punctuationRe)?.length ?? 0,
            isAction = el.closest(actionSelector),
            font = style.font,
            fontSize = Number(style.fontSize.slice(0, -2)),
            approxCharArea = (fontSize * fontSize),
            area = (approxCharArea * txt.length) / windowArea,
            len = txt.length,
            [textLen, actionTextLen] = isAction ? [0, len] : [len, 0],
            [textArea, actionTextArea] = isAction ? [0, area] : [area, 0];
      pCountByFont[style.font] = (pCountByFont[style.font] ?? 0) + pCount;
      for (let el = n.parentElement; el; el = el.parentElement) {
        this.set(el, {actionTextLen, textLen, textArea, actionTextArea, pCount, fontSize, font, textCount: 1});
      }
    }

    const textFonts = {};
    for (let font in pCountByFont) {
      if (pCountByFont[font] > this.max.pCount / 100) textFonts[font] = pCountByFont[font];
    }
    // calculate element metrics and collect candidates
    const candidates = [], els = [];
    const elWalker = document.createTreeWalker(document, NodeFilter.SHOW_ELEMENT);
    const number = (...vs) => vs.find(v => !isNaN(v)) ?? 0
    while (elWalker.nextNode()) {
      const el = elWalker.currentNode;
      // TODO: find a better place to remove attributes
      if (el.style) el.removeAttribute("style");

      // TODO: wait for images to be loaded / force lazy images to load
      const area = number(el.width * el.height,
                          el.offsetHeight * el.offsetWidth,
                          el.clientHeight * el.clientWidth) / windowArea
      const tagScore = el.closest("main, article, section") ? 1 : el.closest("footer, header, nav, aside") ? -1 : 0
      const longCount = el._ && el._.pCount > 3 ? 1 : 0;
      const shortCount = !el._ || (el._.textLen || 0) < 10 ? 1 : 0;
      const isInAction = !el.closest(actionSelector);
      const isAction = el.matches(actionSelector);
      const bpCount = el.matches("img, svg, button, [role=button], input, textarea") && area <= 0.05 ? 1 : 0;
      const actionArea = isAction ? area : 0;
      const mediaArea = (!bpCount && el.matches("img, svg, video")) ? area : 0;
      const bpArea = bpCount ? area : 0;
      let depth = 0;
      for (let p = el.parentElement; p; p = p.parentElement) {
        this.set(p, {childCount: 1, bpCount, longCount, shortCount, mediaArea, bpArea, actionArea})
        depth++;
      }
      this.set(el, {depth, area, mediaArea, bpArea, actionArea, tagScore});
      if (area > 0.2) candidates.push(el);
      els.push(el)
    }

    for (let el of els) {
      // Object.assign(el.dataset, el._); // TODO: only debug
      const {
        childCount = 0,
        textLen = 0, textArea = 0,
        actionTextArea = 0, actionArea = 0,
        area = 0, mediaArea = 0, bpArea = 0,
        font = "",
      } = el._;
      const contentArea = Math.max(area - mediaArea, 0);
      if (!contentArea) continue;
      const textDensity = textArea / contentArea;
      const actionTextDensity = actionTextArea / actionArea;
      const bpDensity = bpArea / contentArea;
      const isCode = !!el.closest("pre, code");
      const isTextFont = !!textFonts[font];
      const isBoilerplate = !isCode && !isTextFont && (childCount > 5 && textDensity <= 0.5 && mediaArea/area <= 0.5) &&
            ((!isNaN(actionTextDensity) && actionTextDensity <= 1) || bpDensity >= textDensity);
      // Object.assign(el.dataset, {isCode, isTextFont, contentArea, bpDensity, textDensity, actionTextDensity, isBoilerplate});
      // if (isBoilerplate && !isCode) el.style = "border: 1px solid red; opacity: 0.5;";
   }

    for (let el of candidates) el.scores = this.score(el);
    candidates.sort((a, b) => b.scores.total - a.scores.total);
    return candidates;
  }

  set(el, kvs) {
    el._ ??= {}
    for (let k in kvs) {
      if (typeof kvs[k] !== "number") el._[k] = kvs[k];
      else {
        el._[k] = (el._[k] ?? 0) + (isNaN(kvs[k]) ? 0 : kvs[k]);
        this.max[k] = Math.max(this.max[k] ?? 0, el._[k]);
      }
    }
  }

  isVisible(el) {
    if (!("isVisible" in el)) {
      el.isVisible = el.checkVisibility({opacityProperty: true, visibilityProperty: true});
    }
    return el.isVisible;
  }

  score(el) {
    const scores = {};
    for (let k in this.weights) {
      scores[k] = this.max[k] ? (((el._[k] ?? 0) / this.max[k]) * this.weights[k]) : 0;
    }
    scores.total = Object.entries(scores).reduce((sum, [k, v]) => sum + v, 0)
    return scores;
  }

  inlineIMG(img) {
    if (this.debug) return
    try {
      const c = document.createElement("canvas");
      c.width = img.naturalWidth, c.height = img.naturalHeight;
      c.getContext("2d").drawImage(img, 0, 0);
      img.setAttribute("height", img.clientHeight);
      img.setAttribute("width", img.clientWidth);
      img.src = c.toDataURL("image/webp", 0.8);
      if (img.closest("picture")) img.closest("picture").replaceWith(img);
    } catch (err) {
      console.error("img", img, err)
    }
  }

  inlineSVG(svg) {
    if (this.debug) return
    const img = new Image();
    const xml = new XMLSerializer().serializeToString(svg);
    img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(xml);
    img.setAttribute("height", svg.clientHeight)
    img.setAttribute("width", svg.clientWidth);
    svg.replaceWith(img);
  }

  parseMeta(document) {
    const kvs = {"html:title": document.title};
    for (let m of [...document.querySelectorAll("meta[name], meta[property]")]) {
      const k = m.getAttribute("property") || m.getAttribute("name");
      if (k === "application/ld+json") {
        try { kvs["ld"] = JSON.parse(m.content) } catch(err) { console.log("ld", err) }
      } else {
        kvs[k] = m.content;
      }
    }
    return {
      kvs,
      title: kvs.ld?.headline || kvs["og:title"] || kvs["title"] || kvs["html:title"],
      author: kvs.ld?.author?.name || kvs["author"],
      createdAt: kvs.ld?.dateCreated || kvs["article:published_time"],
    }
  }
}

export function extract(document, debug) {
  try {
    const e = new Extractor(debug)
    return e.run(document)
  } catch(err) {
    console.log(err)
  }
}
