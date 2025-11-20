package systemd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

var journalSocket = &net.UnixAddr{Name: "/run/systemd/journal/socket", Net: "unixgram"}
var journalConnection = sync.OnceValues(func() (*net.UnixConn, error) {
	return net.ListenUnixgram("unixgram", &net.UnixAddr{Net: "unixgram"})
})

// https://systemd.io/JOURNAL_NATIVE_PROTOCOL/
// http://www.freedesktop.org/software/systemd/man/systemd.journal-fields.html
func JournalLog(msg, priority string, kvs map[string]string) error {
	c, err := journalConnection()
	if err != nil {
		return err
	}
	w := &bytes.Buffer{}
	writeJournalKV(w, "MESSAGE", msg)
	writeJournalKV(w, "PRIORITY", priority)
	for k, v := range kvs {
		writeJournalKV(w, k, v)
	}
	_, _, err = c.WriteMsgUnix(w.Bytes(), nil, journalSocket)
	return err
}

func writeJournalKV(w io.Writer, k, v string) {
	if !strings.ContainsRune(v, '\n') {
		fmt.Fprintf(w, "%s=%s\n", k, v)
	} else {
		fmt.Fprint(w, k)
		binary.Write(w, binary.LittleEndian, uint64(len(v)))
		fmt.Fprint(w, v)
	}
}
