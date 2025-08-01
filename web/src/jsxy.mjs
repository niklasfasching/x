// TODO: pass children as prop in renderChild to fns
const attrs = new Set("list", "form", "selected");
let ctx, style;

export function html(strings, ...values) {
  let $ = "child", xs = [{children: []}], tmp = "";
  for (let s of strings) {
    for (let i = 0, c = s[0]; i < s.length; i++, c = s[i]) {
      if ($ === "child" && c === "<") {
        if (tmp.trim() !== "") {
          xs[xs.length-1].children.push(tmp);
        }
        if (s[i+1] === "/") {
          $ = "close";
        } else {
          xs.push({children: [], props: []});
          $ = "#$:.-".includes(s[i+1]) ? "key" : "open";
        }
      } else if (($ === "close" && c === ">")) {
        const x = xs.pop(), props = x.props.length || typeof x.tag === "function" ? {} : undefined;
        if (!("tag" in x)) x.tag = "div";
        if (tmp && tmp !== "/" && tmp.slice(1) !== x.tag) {
          throw new Error(`unexpected <${tmp}> in <${x.tag}>`);
        }
        for (let i = 0; i < x.props.length; i += 2) {
          const k = x.props[i], v = x.props[i+1];
          if (k === "...") Object.assign(props, v);
          else if (k[0] === ".") v && (props.classList = (props.classList || "") + " " + k.slice(1));
          else if (k[0] === "#") v && (props.id = k.slice(1));
          else if (k[0] === "$") v && (x.ref = k.slice(1));
          else if (k[0] === "-" && k[1] === "-") props.style = (props.style || "") + `;${k}:${v};`;
          else props[k] = v;
        }
        x.props = props;
        xs[xs.length-1].children.push(x);
        $ = "child";
      } else if (($ === "open" || $ === "key" || $ === "value") &&
                 (c === " " || c === "\n" || c === ">" || (c === "/" && s[i+1] === ">"))) {
        if ($ === "open") {
          xs[xs.length-1].tag = tmp;
        } else if ($ === "key" && tmp) {
          xs[xs.length-1].props.push(tmp, true);
        } else if ($ === "value") {
          xs[xs.length-1].props.push(tmp);
        }
        $ = c === "/" ? "close" : c === ">" ? "child" : "key";
      } else if ($ === "key" && c === "=") {
        xs[xs.length-1].props.push(tmp);
        $ = s[i+1] === `'` || s[i+1] === `"` ? "quoted-value" : "value";
      } else if ($ === "quoted-value" && (c === tmp[0])) {
        xs[xs.length-1].props.push(tmp.slice(1));
        $ = "key";
      } else {
        tmp += c;
        continue
      }
      tmp = "";
    }
    let v = values.shift();
    if ($ !== "child" && v != null && v !== false) {
      tmp = tmp ? tmp + v : v;
    } else if ($ === "child") {
      if (tmp.trim() || values.length) {
        xs[xs.length-1].children.push(tmp);
        tmp = "";
      }
      if (Array.isArray(v)) xs[xs.length-1].children.push(...v);
      else if (v != null && v !== false) xs[xs.length-1].children.push(v);
    }
  }
  const children = xs.pop().children;
  if (tmp.trim()) {
    throw new Error(`leftovers: '${tmp}'`);
  } else if (xs.length) {
    throw new Error(`leftovers: ${JSON.stringify(xs)}`);
  } else if (children.length > 1) {
    throw new Error (`more than one top lvl node: ${JSON.stringify(children)}`)
  }
  return children[0];
}

export function css(strings, ...values) {
  if (!style) style = document.head.appendChild(document.createElement("style"));
  style.innerHTML += String.raw(strings, ...values);
}

export function useState(value) {
  return getHook({value}).value;
}

export function useEffect(mount, deps = []) {
  const h = getHook({mount}), changed = !h.deps || !h.vnode || h.vnode.node !== ctx.node || h.deps.some((v, i) => v !== deps[i]);
  if (changed) ctx.queue.push(async () => h.unmount = await (h.unmount?.(), h.mount()));
  h.vnode = ctx.vnode, h.deps = deps;
}

export function useAsync(f, deps = []) {
  const h = getHook({loading: true}), vnode = ctx.vnode, changed = !h.deps || h.deps.some((v, i) => v !== deps[i]);
  if (changed) f().then((v) => h.value = v, (err) => h.err = err).then(() => { delete h.loading, render(vnode) });
  h.deps = deps;
  return h;
}

export function useRoute(f) {
  route.hooks[ctx.k] = f;
  if (history.state?.scrollTop) ctx.queue.push(() => f().scrollTop = history.state.scrollTop)
}

export function getHook(v) {
  if (!ctx.k) throw new Error(`getHook from unkeyed component`);
  if (!ctx.parentNode.hooks[ctx.k]) ctx.parentNode.hooks[ctx.k] = [];
  const khs = ctx.parentNode.hooks[ctx.k], h = khs[ctx.i++];
  if (!ctx.parentNode.newHooks[ctx.k]) ctx.parentNode.newHooks[ctx.k] = khs;
  return h ? h : khs[khs.push(v) - 1];
}

export function render(vnode, parentNode) {
  if (parentNode) return void renderChildren(parentNode, vnode, vnode);
  vnode = vnode.self || vnode.component || vnode;
  if (!vnode.node) return renderChild(document.createElement("div"), vnode);
  renderChild(vnode.node.parentNode, vnode, vnode.node, vnode);
}

function renderChildren(parentNode, vnodes, component, ns) {
  if (!Array.isArray(vnodes)) vnodes = [vnodes];
  if (!parentNode.hooks) parentNode.hooks = {};
  let nodes = [...parentNode.childNodes], end = vnodes.length;
  parentNode.newHooks = {};
  for (let i = 0; i < end; i++) {
    let vnode = vnodes[i], node = nodes[i], tag = node?.vnode?.ctag || node?.vnode?.tag;
    if (tag && vnode.tag !== tag) end = i;
    else renderChild(parentNode, vnode, node, component, null, ns);
  }
  for (let i = vnodes.length - 1, j = end; i >= end; i--, j++) {
    renderChild(parentNode, vnodes[i], nodes[j], component, nodes[end], ns);
  }
  for (let k in parentNode.hooks) {
    if (!(k in parentNode.newHooks)) for (let h of parentNode.hooks[k]) h.unmount?.();
  }
  parentNode.hooks = parentNode.newHooks;
  for (let i = nodes.length-1; i >= vnodes.length; i--) unmount(nodes[i], true);
}

function renderChild(parentNode, vnode, node, component, refNode, ns) {
  if (vnode == null) return unmount(node, true);
  else if (vnode instanceof HTMLElement) replaceNode(parentNode, vnode, node, refNode);
  else if (!vnode.tag) return createTextNode(parentNode, vnode, node, refNode);
  else if (typeof vnode.tag !== "function") {
    ns = vnode.props?.xmlns || ns
    if (!node || vnode.tag !== node.vnode?.tag) node = createNode(parentNode, vnode.tag, node, refNode, ns);
    renderChildren(node, vnode.children, component, ns);
    setProperties(node, vnode, component, ns);
  } else {
    renderChildren(node, vnode.children, component, ns);
    vnode.props.$ = {self: vnode, app: component.props.$?.app || component};
    ctx = {parentNode, node, vnode, i: 0, k: vnode.props.key || vnode.props.id, queue: []};
    let _vnode = vnode.tag(vnode.props), _ctx = ctx;
    _vnode.ctag = vnode.tag, node = renderChild(parentNode, _vnode, node, vnode, refNode, ns), vnode.node = node;
    for (let f of _ctx.queue) f();
  }
  if (vnode.ref) component.props.$[vnode.ref] = node;
  return node;
}

function unmount(node, rm) {
  if (!node) return;
  else if (!rm) for (let k in node.hooks) for (let h of node.hooks[k]) h.unmount?.();
  for (let child of node.childNodes) unmount(child);
  if (rm) node.remove();
}

function createNode(parentNode, tag, node, refNode, ns) {
  const newNode = ns ? document.createElementNS(ns, tag) : document.createElement(tag);
  return replaceNode(parentNode, newNode, node, refNode);
}

function createTextNode(parentNode, vnode, node, refNode) {
  if (node?.nodeType === 3) node.data = vnode;
  else node = replaceNode(parentNode, document.createTextNode(vnode), node, refNode);
  return node;
}

function replaceNode(parentNode, newNode, oldNode, refNode) {
  if (oldNode) unmount(oldNode), parentNode.replaceChild(newNode, oldNode);
  else if (refNode) parentNode.insertBefore(newNode, refNode);
  else parentNode.append(newNode);
  return newNode;
}

function setProperties(node, vnode, component, ns) {
  for (let k in vnode.props) {
    if (node.vnode?.props?.[k] !== vnode.props[k]) setProperty(node, k, vnode.props[k], ns);
  }
  if (node.vnode) {
    for (let k in node.vnode.props) {
      if (!vnode.props || !(k in vnode.props)) setProperty(node, k, "");
    }
  }
  vnode.node = node, node.vnode = vnode, node.component = component;
}

function setProperty(node, k, v, ns) {
  if (k[0] == "o" && k [1] == "n") setEventListener(node, k.slice(2), v);
  else if (k[0] === "@") setEventListener(node, k.slice(1), v);
  else if (k[0] === "!") node[k.slice(1)] = v;
  else if (k in node && !attrs.has(k) && !ns) node[k] = v == null ? "" : v;
  else if (v == null || v === false) node.removeAttribute(k);
  else node.setAttribute(k, v);
}

function setEventListener(node, type, v) {
  if (v) node.addEventListener(type, eventListener);
  else node.removeEventListener(type, eventListener);
}

function eventListener(e) {
  const props = this.vnode.props;
  const v = props["on"+e.type] || props["@"+e.type];
  if (Array.isArray(v)) v[0](e, ...v.slice(1));
  else v(e);
  if (props["@"+e.type]) render(e.target.component);
}

export function route(routes, parentNode) {
  route.hooks = {}
  route.go = (href) => {
    if (!route.params) return
    const f = route.hooks[route.params.key];
    history.replaceState({scrollTop: f?.().scrollTop}, "");
    history.pushState({}, "", href);
    renderRoute(routes, parentNode);
  };
  window.addEventListener("popstate", () => renderRoute(routes, parentNode));
  parentNode.addEventListener("click", (e) => {
    const a = e.target.closest("a"), href = a?.getAttribute("href"),
          isRoute = href?.slice(0, 2) === "?/" || href?.slice(0, 3) === "/?/";
    if (isRoute && !e.ctrlKey) (route.go(a.href), e.preventDefault());
  });
  renderRoute(routes, parentNode);
}

function renderRoute(routes, parentNode) {
  const q = location.search, [path] = q.slice(1).split("&", 1),
        params = Object.fromEntries(new URLSearchParams(q.slice(path.length+1)));
  for (let [r, tag] of Object.entries(routes)) {
    if (matchRoute(r, path, params)) {
      window.scrollTo(0, 0);
      document.activeElement?.blur?.();
      Object.assign(route, {path, params});
      return void render({tag, props: params}, parentNode);
    }
  }
  route.go("?/");
}

function matchRoute(route, path, params) {
  const r = new RegExp("^" + route.replace(/\/?$/, "/?$").replace(/\/{(.+?)}/g, (_, x) =>
    x.startsWith("...") ? `/(?<${x.slice(3)}>(.*)?)` : `/(?<${x}>[^/]+)`
  ));
  const match = r.exec(path);
  if (match) {
    params.key = route;
    for (const k in match.groups) params[k] = decodeURIComponent(match.groups[k]);
    return true;
  }
}
