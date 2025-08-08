package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/niklasfasching/x/soup"
	"github.com/niklasfasching/x/sqlite"
	"github.com/niklasfasching/x/telegram"
)

type App struct {
	*sqlite.DB
	*telegram.T
	*http.Client
	GroupID             int
	SessionsURL, Cookie string
}

type Session struct {
	Title, Hosts, Description string
}

func main() {
	a, err := New(true)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(a.CreateTopics())
}

func New(dev bool) (*App, error) {
	db, err := sqlite.New("sessions.db", []string{
		"CREATE TABLE sessions(title, thread_id, data)",
		"ALTER TABLE sessions ADD message_id",
	}, nil, nil, true)
	if err != nil {
		return nil, err
	}
	tr := soup.Transport{}
	if dev {
		tr.Cache = &soup.FileCache{Root: "http"}
	}
	c, err := tr.Client()
	if err != nil {
		return nil, err
	}
	t := &telegram.T{
		Token:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		Client: http.DefaultClient,
	}
	gid, err := strconv.Atoi(os.Getenv("TELEGRAM_GROUP_ID"))
	if err != nil {
		return nil, err
	}
	return &App{
		Client:      c,
		DB:          db,
		T:           t,
		GroupID:     gid,
		Cookie:      os.Getenv("LWCW_COOKIE"),
		SessionsURL: os.Getenv("LWCW_SESSIONS_URL"),
	}, nil
}

func (a *App) GetSchedule() (map[string]Session, error) {
	m := map[string]Session{}
	req, err := http.NewRequest("GET", a.SessionsURL, nil)
	if err != nil {
		return m, err
	}
	req.Header.Add("Cookie", a.Cookie)
	res, err := a.Do(req)
	if err != nil {
		return m, err
	}
	defer res.Body.Close()
	d, err := soup.Parse(res.Body)
	if err != nil {
		return m, err
	}
	for _, tr := range d.All("tr") {
		title := tr.First("td:nth-of-type(1)").TrimmedText()
		hosts := tr.First("td:nth-of-type(2)").TrimmedText()
		description := tr.First("td:nth-of-type(3)").TrimmedText()
		// TODO: html does currently not contain the id
		// we're abusing the title as the id...
		if title == "" {
			continue
		} else if _, exists := m[title]; exists {
			return m, fmt.Errorf("Multiple sessions named %q exist", title)
		}
		m[title] = Session{
			Title:       title,
			Hosts:       hosts,
			Description: description,
		}
	}
	return m, nil
}

func (a *App) CreateTopics() error {
	m, err := a.GetSchedule()
	if err != nil {
		return err
	}
	test, i := false, 0
	for title, session := range m {
		i++
		row, err := sqlite.QueryOne[sqlite.Map[string]](a.DB, "SELECT * FROM sessions WHERE title = ?", title)
		if err != nil && !errors.Is(err, sqlite.NoResultsErr) {
			return fmt.Errorf("%q: %w", title, err)
		}
		if row["title"] != "" {
			log.Printf("Skipping existing %q", title)
			continue
		} else if test {
			log.Printf("Missing %q", title)
			continue
		}
		rt := struct {
			Result struct{ Message_thread_id int }
		}{}
		err = a.T.POST("createForumTopic", url.Values{
			"chat_id": {fmt.Sprintf("%d", a.GroupID)},
			"name":    {title},
		}, &rt)
		if err != nil {
			return fmt.Errorf("%q: %w", title, err)
		}
		rm := struct{ Result struct{ Message_id int } }{}
		err = a.T.POST("sendMessage", url.Values{
			"chat_id":           {fmt.Sprintf("%d", a.GroupID)},
			"message_thread_id": {fmt.Sprintf("%d", rt.Result.Message_thread_id)},
			"text":              {session.Description},
		}, &rm)
		if err != nil {
			return fmt.Errorf("%q: %w", title, err)
		}
		bs, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("%q: %w", title, err)
		}
		_, err = sqlite.Exec(a.DB,
			"INSERT INTO sessions (title, thread_id, message_id, data) VALUES (?, ?, ?, ?)",
			title, rt.Result.Message_thread_id, rm.Result.Message_id, string(bs))
		if err != nil {
			return fmt.Errorf("%q: %w", title, err)
		}
		log.Println("Finished", i, title, rt.Result.Message_thread_id)
		time.Sleep(5 * time.Second)
	}
	return nil
}
