package i18n

// Msg is a translation key plus optional fmt arguments, safe to store across locale changes.
type Msg struct {
	Key  string
	Args []any
}

func M(key string, args ...any) Msg {
	return Msg{Key: key, Args: args}
}

func (m Msg) IsZero() bool {
	return m.Key == ""
}

func (m Msg) String() string {
	if m.Key == "" {
		return ""
	}
	if len(m.Args) == 0 {
		return T(m.Key)
	}
	return Tf(m.Key, m.Args...)
}
