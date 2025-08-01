// https://developers.google.com/cast/docs/web_receiver/core_features#custom_messages
const nsSystem = "urn:x-cast:com.google.cast.system"
const nsCustom = "urn:x-cast:com.x.cast";

export class Controller {
  constructor(options, handleMessage) {
    this.handleMessage = handleMessage;
    this.clientId = `${options.clientId || Date.now()}`;
    this.req = new PresentationRequest(`cast:${options.appId}?${new URLSearchParams(Object.assign({
      clientId: this.clientId,
      launchTimeout: 10000,
      autoJoinPolicy: "origin_scoped",
    }, options))}`);
  }

  get isConnected() { return this.c?.state === "connected" }

  async connect() {
    if (this.isConnected) return this.status;
    this.ready = Promise.withResolvers();
    this.c = await this.req.reconnect("auto-join").then(c => c, () => null) || await this.req.start();
    this.c.onconnect = () => this.sendMessage("client_connect", this.clientId);
    this.c.onmessage = (event) => this.onMessage(event);
    this.c.i = 0;
    await this.ready.promise;
    return this.status;
  }

  disconnect() {
    this.c = void this.c?.terminate();
  }

  onMessage(event) {
    const {type, message} = JSON.parse(event.data);
    if (!message?.sessionId) return
    this.sessionId = message.sessionId;
    const kv = type === "app_message" ?
          JSON.parse(message.message || "{}") :
          {type: `receiver_${type}`, value: message};
    if (kv.type === "receiver_update_session" || kv.type === "receiver_new_session") {
      this.status = kv.value;
      this.ready.resolve();
    } else {
      this.handleMessage(kv.type, kv.value);
    }
  }

  send(type, value) {
    return this.sendMessage("app_message", {type, value})
  }

  async sendMessage(type, v) {
    if (!this.isConnected) await this.connect();
    this.c.send(JSON.stringify({
      type,
      sequenceNumber: this.c.i++,
      clientId: this.clientId,
      message: type !== "app_message" ? `${v}` : {
        sessionId: this.sessionId,
        namespaceName: nsCustom,
        message: JSON.stringify(v),
      },
    }));
  }
}

export class EvalController extends Controller {
  constructor(options, f = (k, v) => console.log("REMOTE CMD", k, v)) {
    super(options, (type, {id, ...v}) => {
      if (id) {
        if (v.err) this.cmds[id].reject(v.err);
        else this.cmds[id].resolve(v.result);
        delete this.cmds[id];
      } else if (type === "log") {
        const {method, args} = v;
        console[method]("REMOTE", ...args);
      } else f(type, v);
    });
    Object.assign(this, {options, cmds: {}, id: 0});
    if (options.local) this.sandbox = new Sandbox(document.body);
  }

  eval(code, args = {}) {
    code = typeof code === "function" ? `(${code})()` : code;
    for (let k in args) args[k] = JSON.stringify(args[k], (k, v) => {
      return typeof v === "function" ? `${v}` : JSON.stringify(v);
    });
    if (this.sandbox) return this.sandbox.eval(code, args);
    return this.cmd("eval", {code, args});
  }

  cmd(type, value = {}) {
    const id = this.id++;
    this.cmds[id] = Promise.withResolvers();
    this.send(type, JSON.stringify({...value, id}));
    return this.cmds[id].promise;
  }

  connect() {
    if (!this.options.local) return super.connect();
  }
}

export class Receiver {
  constructor(handleMessage) {
    this.handleMessage = handleMessage;
    if (document.readyState === "complete") this.connect();
    else document.onload = this.connect();
  }

  connect() {
    this.c = window.cast.__platform__.channel;
    this.c.open(() => {
      this.send("SystemSender", nsSystem, {
        type: "ready",
        statusText: "ready",
        activeNamespaces: [nsCustom],
        version: "0.0.1",
        messagesVersion: "1.0",
      });
    }, (message, ...args) => this.onMessage(message, args));
  }

  send(senderId, namespace, v) {
    return this.c.send(JSON.stringify({
      senderId,
      namespace,
      data: v === Object(v) ? JSON.stringify(v) : `${v}`,
    }));
  }

  onMessage(message, args) {
    const m = JSON.parse(message);
    const kvs = JSON.parse(m.data || "{}");
    const {senderId, type, value} = m.namespace === nsSystem ?
          {senderId: kvs.senderId, type: "receiver_"+kvs.type, value: kvs} :
          {senderId: m.senderId, type: kvs.type, value: kvs.value};
    this.handleMessage(type, value, (type, value) => this.send(senderId, nsCustom, {type, value}));
  }
}

// TODO: refactor to allow handling non eval messages, just any message
// and provide eval handler as default
// could have cast receiver instead receive srcdoc for iframe and forward all messages to iframe?
// Sandbox would ideally be self contained
export class Sandbox {
  constructor(parentElem, url = location.href, onLog = ({method, args}) => console[method](...args)) {
    this.iframe = document.createElement("iframe");
    this.iframe.sandbox.add("allow-scripts");
    this.iframe.allow = "autoplay"
    this.ready = new Promise((onload, onerror) => {
      Object.assign(this.iframe, {onload, onerror});
    });
    this.iframe.srcdoc = `
      <style>
        html, body { height: 100%; width: 100%; margin: 0; padding: 0; }
      </style>
      <body>
        <script>
          ${evalInWindow};
          new (${ConsoleToaster})();
          window.addEventListener("message", async (e) => {
            const {id, code, args} = JSON.parse(e.data);
            const {result, err} = await evalInWindow(window, code, args);
            window.parent.postMessage(JSON.stringify({id, result, err}), "${url}");
          });
        </script>
      </body>`;
    parentElem.append(this.iframe);
    window.addEventListener("message", (e) => {
      if (e.source === this.iframe.contentWindow) this.onIframeMessage(e);
    });
    Object.assign(this, {onLog, evals: {}, id: 0});
  }

  onIframeMessage(e) {
    const {id, result, err, method, args} = JSON.parse(e.data);
    if (id != null) this.evals[id]({result, err});
    else if (this.onLog) this.onLog({method, args});
  }

  // TODO: send. and we have a handler for eval
  // if it's anything but eval, we create an event? how do we let users define a handler?
  // onreceive
  async eval(code, args = {}) {
    await this.ready;
    const id = this.id++;
    const promise = new Promise((resolve) => this.evals[id] = resolve);
    this.iframe.contentWindow.postMessage(JSON.stringify({id, code, args}), '*');
    return promise;
  }
}

async function evalInWindow(window, code, args = {}) {
  try {
    const Function = window.Function,
          params = Object.keys(args),
          values = Object.values(args).map(arg => {
            return new Function(`return ${JSON.parse(arg)}`)();
          });
    const f = new Function(...params, `return (${code})`);
    return {result: await f(...values)};
  } catch (err) {
    return {err: err.stack};
  }
}

export class ConsoleToaster {
  constructor(options = {}) {
    this.timeout = options.timeout || 5000;
    this.console = window.console;
    this.el = document.body.appendChild(document.createElement("div"));
    Object.assign(this.el.style, {
      position: "fixed",
      bottom: "2em",
      right: "2em",
      width: "33vw",
      zIndex: "10000",
      display: "flex",
      flexDirection: "column-reverse",
      alignItems: "flex-end",
      gap: "1em",
    });
    window.console = new Proxy(this, {
      get(target, k, receiver) {
        if (typeof target.console[k] !== "function") return Reflect.get(target.console, k, receiver);
        return (...args) => {
          target.showToast(k, args)
          return target.console[k](...args);
        };
      }
    });
  }

  showToast(k, args) {
    const toastEl = document.createElement("div");
    toastEl.textContent = args.map(arg => {
      if (typeof arg !== "object") return String(arg);
      try { return JSON.stringify(arg, null, 2) }
      catch (e) { return String(arg) }
    }).join(" ");
    const colors = {
      debug: "rgba(230, 230, 230, 0.85)",
      log: "rgba(250, 250, 250, 0.85)",
      info: "rgba(230, 240, 230, 0.85)",
      warn: "rgba(255, 240, 200, 0.85)",
      error: "rgba(255, 220, 230, 0.85)",
    };
    Object.assign(toastEl.style, {
      padding: '1em 1.5em',
      background: colors[k],
      borderRadius: "0.5em",
      fontFamily: "monospace",
      opacity: "0",
      width: "100%",
      transition: "opacity 0.5s ease-in-out, transform 0.5s ease-in-out",
      transform: "translateY(2em)",
      wordBreak: "break-all",
      boxShadow: "0 0.5em 1em rgba(0, 0, 0, 0.3)",
      whiteSpace: "pre-wrap",
    });
    this.el.appendChild(toastEl);
    requestAnimationFrame(() => {
      toastEl.style.opacity = '1';
      toastEl.style.transform = 'translateY(0)';
    });
    setTimeout(() => {
      toastEl.style.opacity = '0';
      toastEl.style.transform = 'translateY(20px)';
      toastEl.addEventListener('transitionend', () => {
        toastEl.remove();
      }, {once: true});
    }, this.timeout);
  }

  destroy() {
    console = this.console
    this.el.remove();
  }
}
