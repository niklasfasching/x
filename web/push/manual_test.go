package push

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestManualIntegration(t *testing.T) {
	if os.Getenv("MANUAL") != "1" {
		t.Skip("Skipping manual integration test; set MANUAL=1 to run")
	}
	s, err := New("admin@localhost", GeneratePrivateKey())
	if err != nil {
		t.Fatal(err)
	}
	currentSub := &Sub{}
	http.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(swJS))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(indexHTML))
	})
	http.HandleFunc("/api/vapid-key", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(s.Pub))
	})
	http.HandleFunc("/api/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(currentSub); err != nil {
			http.Error(w, err.Error(), 400)
		} else {
			fmt.Printf("Subscribed: %+v\n", currentSub)
		}
	})
	http.HandleFunc("/api/notify", func(w http.ResponseWriter, r *http.Request) {
		log.Println("NOTIFY", currentSub)
		if currentSub == nil {
			http.Error(w, "No subscription", 400)
			return
		}
		msg := fmt.Sprintf("Test notification at %s", time.Now().Format(time.Kitchen))
		if err := s.Send(*currentSub, []byte(msg)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write([]byte("ok"))
	})
	fmt.Println("Listening on http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		t.Fatal(err)
	}
}

const swJS = `
  self.addEventListener("push", (e) => {
    const body = e.data ? e.data.text() : "no payload";
    e.waitUntil(self.registration.showNotification("Notification", {body}));
  });`

const indexHTML = `
  <button id="sub">Subscribe</button>
  <button id="notify" disabled>Notify Me</button>
  <pre id="log"></pre>
  <script>
  const log = m => document.getElementById('log').textContent += m + '\n';
  const b64 = (s) => new Uint8Array([...atob(s.replace(/-/g, '+').replace(/_/g, '/'))]
    .map(c => c.charCodeAt(0)));
  let sub;
  document.getElementById('sub').onclick = async () => {
    const sw = await navigator.serviceWorker.register('sw.js');
    const key = await (await fetch('/api/vapid-key')).text();
    try {
      sub = await sw.pushManager.subscribe({
        userVisibleOnly: true, applicationServerKey: b64(key),
      });
    } catch(err) {
      sub = await sw.pushManager.getSubscription();
      await sub.unsubscribe();
      sub = await sw.pushManager.subscribe({
        userVisibleOnly: true, applicationServerKey: b64(key),
      });
    }
    await fetch('/api/subscribe', {method: 'POST', body: JSON.stringify(sub)});
    log('Subscribed!');
    document.getElementById('notify').disabled = false;
  };
  document.getElementById('notify').onclick = () =>
    fetch('/api/notify', { method: 'POST' });
  </script>`
