package util

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

func LoadConfig(c any) error {
	rt, rc := reflect.TypeOf(c).Elem(), reflect.ValueOf(c).Elem()
	for i := 0; i < rt.NumField(); i++ {
		rft := rt.Field(i)
		s, ok := os.LookupEnv(rft.Name)
		if !ok && !rc.Field(i).IsZero() {
			continue
		} else if !ok {
			return fmt.Errorf("failed to lookup field %q in env", rft.Name)
		}
		if rft.Type.Kind() == reflect.String {
			rc.Field(i).SetString(s)
		} else if err := json.Unmarshal([]byte(s), rc.Field(i).Addr().Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal %q(%s) from %q", rft.Name, rft.Type, s)
		}
	}
	return nil
}
