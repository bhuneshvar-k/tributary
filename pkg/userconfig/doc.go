// Package userconfig manages the user-level configuration file
// (~/.tributary/config.yaml), which persists settings that would
// otherwise require repeated CLI flags or environment variables.
//
// Config is optional — commands continue to work without it.
// Precedence: CLI flags > environment variables > config file.
package userconfig
