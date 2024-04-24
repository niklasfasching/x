// https://developers.google.com/cast/docs/web_receiver/core_features#custom_messages
const nsSystem = "urn:x-cast:com.google.cast.system"
const nsCustom = "urn:x-cast:com.cast.custom";

export function newReceiver(f, debug) {
  if (debug) initDebug("receiver");
  const init = () => {
    const c = window.cast.__platform__.channel, send = (senderId, namespace, v) => {
      c.send(JSON.stringify({
        senderId,
        namespace,
        data: v === Object(v) ? JSON.stringify(v) : `${v}`,
      }));
    };
    c.open(() => {
      send("SystemSender", nsSystem, {
        type: "ready",
        statusText: "READY",
        activeNamespaces: [nsCustom],
        version: "2.0.0.0",
        messagesVersion: "1.0",
      });
      send("SystemSender", nsSystem, {
        type: "startheartbeat",
        maxInactivity: 5,
      });
    }, (message) => {
      const m = JSON.parse(message);
      const kvs = JSON.parse(m.data || "{}");
      const {senderId, type, value} = m.namespace === nsSystem ?
            {senderId: kvs.senderId, type: "receiver_"+kvs.type, value: kvs} :
            {senderId: m.senderId, type: kvs.type, value: kvs.value};
      console.log("onmessage", message) // TODO
      if (senderId) f(type, value, (type, value) => send(senderId, nsCustom, {type, value}));
    });
  }
  if (document.readyState === "complete") init();
  else document.onload = init();
}

export async function newController(appId, f, options, debug) {
  if (debug) initDebug("controller");
  const clientId = `${90}`, q = new URLSearchParams(Object.assign({
    clientId,
    launchTimeout: 2000,
    autoJoinPolicy: "page_scoped",
  }, options));
  const req = new PresentationRequest(`cast:${appId}?${q}`);
  navigator.presentation.defaultRequest = req;
  let c = await req.reconnect("auto-join").then((c) => c, () => {}), i = 0;
  const start = async () => {
    if (!c) c = await req.start();
    const send = async (type, sessionId, v) => {
      if (!c || c.state !== "connected") {
        const onmessage = c.onmessage;
        c = await req.reconnect("auto-join").then((c) => c, () => {}), i = 0;
        if (!c) c = await req.start();
        c.onmessage = onmessage;
        await new Promise((resolve) => { c.onconnect = resolve; });
      }
      c.send(JSON.stringify({
        type,
        sequenceNumber: ++i,
        clientId,
        message: type !== "app_message" ? `${v}` : {
          sessionId,
          namespaceName: nsCustom,
          message: JSON.stringify(v),
        },
      }));
    }
    c.onconnect = () => send("client_connect", null, clientId);
    c.onmessage = (e) => {
      const {type, message} = JSON.parse(e.data);
      if (message && message.sessionId) {
        const kv = type === "app_message" ?
              JSON.parse(message.message) :
              {type: `receiver_${type}`, value: message};
        f(kv.type, kv.value, (type, value) => send("app_message", message.sessionId, {type, value}));
      }
    }
  };
  if (c) start(c);
  return {
    start,
    stop: () => c && c.terminate(),
  }
}

export function initDebug(t) {
  console.log("DEBUG");
  const c = new BroadcastChannel("cast");
  if (t === "receiver") {
    const connectMessage = JSON.stringify({senderId: "SystemSender", type: "new_session"});
    c.postMessage("CONNECT");
    c.postMessage(connectMessage);
    window.cast = {__platform__: {}}
    window.cast.__platform__.channel = {
      send: (s) => c.postMessage(s),
      open: (init, onMessage) => {
        init();
        c.onmessage = (e) => {
          if (e.data === "CONNECT") return void c.postMessage(connectMessage);
          const {type, message} = JSON.parse(e.data)
          const v = type === "app_message" ?
                {namespace: nsCustom, senderId: "1", data: message.message} :
                {namespace: nsSystem, data: JSON.stringify({...message, type: type})};
          onMessage(JSON.stringify(v));
        };
      }
    }
  } else if (t === "controller") {
    const connectMessage = JSON.stringify({type: "senderconnected", message: {senderId: "1"}});
    c.postMessage("CONNECT")
    c.postMessage(connectMessage)
    const pc = {send: (s) => c.postMessage(s)}
    window.PresentationRequest = class extends PresentationRequest {
      start() { return this.onconnect(); }
      reconnect() { return this.onconnect(); }
      async onconnect() {
        setTimeout(() => pc.onconnect())
        c.onmessage = (e) => {
          if (e.data === "CONNECT") return void c.postMessage(connectMessage);
          const {type, senderId, data, message} = JSON.parse(e.data);
          const v = senderId === "SystemSender" ?
                {type, message: {...message, sessionId: "1"}} :
                {type: "app_message", message: {sessionId: "1", message: data}};
          pc.onmessage({data: JSON.stringify(v)})
        };
        return pc;
      }
    }
  } else {
    throw new Error(`Unknown type: ${t}`);
  }
}
