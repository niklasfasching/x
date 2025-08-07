package slack

import (
	"encoding/json"
)

type MockConnection struct {
	In       []interface{}
	Out      []interface{}
	MockUser User
	serveMux
}

func (c *MockConnection) Start() error {
	for _, v := range c.In {
		bytes, err := json.Marshal(v)
		if err != nil {
			return err
		}
		e := baseEvent{}
		if err := json.Unmarshal(bytes, &e); err != nil {
			return err
		}
		if err := c.callHandler(e.Type+"/"+e.SubType, bytes, false); err != nil {
			return err
		}
	}
	return nil
}

func (c *MockConnection) Send(v interface{}) error {
	c.Out = append(c.Out, v)
	return nil
}

func (c *MockConnection) User() User { return c.MockUser }
