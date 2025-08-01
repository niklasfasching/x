const subs = new Map();
export const db = new Proxy(localStorage, {
  get: (t, k) => JSON.parse(t.getItem(k)),
  set: (t, k, v) => {
    t.setItem(k, JSON.stringify(v));
    if (!publish.active) publish(db, k, v);
    return true;
  },
  deleteProperty: (t, k) => (t.removeItem(k), true),
  ownKeys: (t) => Reflect.ownKeys(t),
  getOwnPropertyDescriptor: (t, k) => Reflect.getOwnPropertyDescriptor(t, k),
});

export const query = new Proxy(searchParams, {
  get: (t, k) => {
    const v = t()[1].get(k);
    return v && (v[0] === "[" || v[0] === "{") ? JSON.parse(v) : v;
  },
  set: (t, k, v) => {
    const [path, q] = t(), sv = Object(v) === v ? JSON.stringify(v) : v;
    q[sv != null && sv !== "" ? "set" : "delete"](k, sv);
    history.replaceState(null, "", "?"+path+(q.size ? "&"+q : "")+location.hash);
    if (!publish.active) publish(query, k, v);
    return true;
  },
  deleteProperty: (t, k) => (query[k] = undefined, true),
  ownKeys: (t) => [...t().keys()],
  getOwnPropertyDescriptor: (t, k) => ({enumerable: 1, configurable: 1}),
});

export function sub(store, k, f) {
  if (!subs.has(store)) subs.set(store, {});
  subs.get(store)[k] = [f].concat(subs.get(store)[k] || []);
  return () => subs.get(store)[k] = subs.get(store)[k].filter(x => x !== f);
}

function publish(store, k, v) {
  publish.active = true;
  if (subs.get(store) && k in subs.get(store)) for (let f of subs.get(store)[k]) f(v);
  delete publish.active;
}


function applyStoreDirective(node, {tag, props}, [type, key], v, data) {
  const store = {query, db}[type];
  if (tag !== "form") throw new Error(`:store on non-form tag '${tag}'`)
  else if (!key || !store) throw new Error("bad key or type in :store:<type>:<key>");
  if (!data) {
    node.addEventListener("submit", (e) => e.preventDefault());
    node.addEventListener("reset", (e) => delete store[key]);
    node.addEventListener("input", (e) => {
      const fd = new FormData(node), m = {};
      iterateForm(node, (k, el, k2) => {
        const vs = fd.getAll(k);
        if (!k2) m[k] = vs[0];
        else if (vs.length) m[k] = Object.fromEntries(vs.map(k => [k, true]));
      });
      store[key] = m;
    });
  }
  const m = store[key];
  iterateForm(node, (k, el, k2) => {
    const v = m && m[k];
    if (k2) for (const x of el) x[k2] = v && v[x.value];
    else if (el.type === "checkbox") el.checked = v;
    else el.value = v == null ? "" : v;
  });
}

function applyIntersectDirective(node, {tag, props}, args, rootMargin = "0% 100%", data) {
  if (!data) {
    const observer = new IntersectionObserver((xs, observer) => {
      for (const x of xs) x.target.classList.toggle("intersecting", x.isIntersecting);
    }, {rootMargin});
    observer.observe(node);
  }
}


function iterateForm(form, f) {
  for (let k of new Set([...form.elements].map(el => el.name).filter(Boolean))) {
    const el = form.elements[k];
    if (!el.multiple && !(!el.type && el[0].type === "checkbox")) f(k, el);
    else f(k, (el.options || el), el.options ? "selected" : "checked");
  }
}
