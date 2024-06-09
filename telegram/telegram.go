package telegram

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/niklasfasching/x/util"
)

type T struct {
	Token  string
	offset int
}

type Update struct {
	Message  `json:"message"`
	UpdateID int `json:"update_id"`
}

type Message struct {
	Text string `json:"text"`
	Chat struct {
		Name string `json:"username"`
		ID   int    `json:"id"`
	} `json:"chat"`
}

var apiURL = "https://api.telegram.org"

func (t *T) Start(onMsg func(Message)) error {
	for {
		us, err := util.Retry(t.GetUpdates, 5, time.Second)
		if err != nil {
			return err
		}
		for _, u := range us {
			onMsg(u.Message)
			t.offset = u.UpdateID + 1
		}
	}
}

func (t *T) GetUpdates() ([]Update, error) {
	u, err := url.Parse(fmt.Sprintf(apiURL+"/bot%s/getUpdates", t.Token))
	if err != nil {
		return nil, err
	}
	vs := url.Values{}
	vs.Add("timeout", "300") // 5 min
	if t.offset != 0 {
		vs.Add("offset", strconv.Itoa(t.offset))
	}
	u.RawQuery = vs.Encode()
	res, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	bs, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	v := struct {
		Ok     bool
		Result []Update
	}{}
	if err := json.Unmarshal(bs, &v); err != nil {
		return nil, err
	} else if !v.Ok {
		return nil, fmt.Errorf("unexpected response: %s", string(bs))
	}
	return v.Result, nil
}

func (t *T) SendMessage(chatID int, kvs ...string) error {
	vs := url.Values{"chat_id": {strconv.Itoa(chatID)}}
	if len(kvs)%2 != 0 {
		return fmt.Errorf("number of kvs must be even: %v", kvs)
	}
	for i := 0; i < len(kvs); i += 2 {
		vs.Add(kvs[i], kvs[i+1])
	}
	return t.POST("sendMessage", vs, nil)
}

func (t *T) POST(method string, vs url.Values, v any) error {
	u := fmt.Sprintf(apiURL+"/bot%s/%s", t.Token, method)
	res, err := http.PostForm(u, vs)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	bs, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	x := struct{ OK bool }{}
	if err, c := json.Unmarshal(bs, &x), res.StatusCode; err != nil || !x.OK || c > 400 {
		return fmt.Errorf("bad response (%v): %s (%q)", c, err, string(bs))
	} else if v == nil {
		return nil
	}
	return json.Unmarshal(bs, v)
}
