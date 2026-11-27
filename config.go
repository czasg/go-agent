package ga

type Config struct {
	MaxTurns    int
	MaxParallel int
}

type Option func(*Agent)

func WithModel(m Model) Option {
	return func(a *Agent) { a.Model = m }
}

func WithName(n string) Option {
	return func(a *Agent) { a.Name = n }
}

func WithDescription(d string) Option {
	return func(a *Agent) { a.Description = d }
}

func WithSystem(s string) Option {
	return func(a *Agent) { a.system = s }
}

func WithTools(tools ...*Tool) Option {
	return func(a *Agent) { a.Tools.Add(tools...) }
}

func WithToolBox(b *ToolBox) Option {
	return func(a *Agent) {
		if b != nil {
			a.Tools = b
		}
	}
}

func WithHooks(hooks ...any) Option {
	return func(a *Agent) { a.Hooks = a.Hooks.Use(hooks...) }
}

func WithEmitter(e Emitter) Option {
	return func(a *Agent) { a.Emitter = e }
}

func WithConfig(c Config) Option {
	return func(a *Agent) {
		if c.MaxTurns != 0 {
			a.cfg.MaxTurns = c.MaxTurns
		}
		if c.MaxParallel != 0 {
			a.cfg.MaxParallel = c.MaxParallel
		}
	}
}

func WithMaxTurns(n int) Option {
	return func(a *Agent) { a.cfg.MaxTurns = n }
}

func WithMaxParallel(n int) Option {
	return func(a *Agent) { a.cfg.MaxParallel = n }
}

func WithState(k string, v any) Option {
	return func(a *Agent) {
		if a.State == nil {
			a.State = map[string]any{}
		}
		a.State[k] = v
	}
}
