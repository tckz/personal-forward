package forward

import "fmt"

type StringArrayFlag []string

func (f *StringArrayFlag) String() string {
	return fmt.Sprintf("%v", *f)
}

func (f *StringArrayFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}
