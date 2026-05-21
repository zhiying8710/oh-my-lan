package config

// LogConfig 是服务端和客户端共用的日志配置。
type LogConfig struct {
	Level  string `yaml:"level"`  // debug / info / warn / error
	Format string `yaml:"format"` // text / json
}
