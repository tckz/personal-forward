package forward

import (
	"fmt"
	"net/http"
	"time"
)

func AsString(v interface{}, err error) (ret string, rerr error) {
	if err != nil {
		return ret, err
	}
	if v == nil {
		return ret, fmt.Errorf("v is nil")
	}

	if s, ok := v.(string); ok {
		return s, nil
	}
	return ret, fmt.Errorf("v is not string")
}

func AsTime(v interface{}, err error) (ret time.Time, rerr error) {
	if err != nil {
		return ret, err
	}

	if v == nil {
		return ret, fmt.Errorf("v is nil")
	}

	if e, ok := v.(time.Time); ok {
		return e, nil
	}
	return ret, fmt.Errorf("v is not time.Time")
}

func AsByte(v interface{}, err error) (ret []byte, rerr error) {
	if err != nil {
		return ret, err
	}

	if v == nil {
		return ret, fmt.Errorf("v is nil")
	}

	if e, ok := v.([]byte); ok {
		return e, nil
	}
	return ret, fmt.Errorf("v is not []byte")
}

func AsInt64(v interface{}, err error) (ret int64, rerr error) {
	if err != nil {
		return ret, err
	}

	if v == nil {
		return ret, fmt.Errorf("v is nil")
	}

	if e, ok := v.(int64); ok {
		return e, nil
	}
	return ret, fmt.Errorf("v is not int64")
}

func AsHeader(v interface{}, err error) (ret http.Header, rerr error) {
	ret = make(http.Header)
	if err != nil {
		return ret, err
	}
	if v == nil {
		return ret, fmt.Errorf("v is nil")
	}

	if m, ok := v.(map[string]interface{}); ok {
		for k, v := range m {
			if vals, ok := v.([]interface{}); ok {
				for _, e := range vals {
					ret.Add(k, fmt.Sprint(e))
				}
			}
		}
	}

	return ret, nil
}
