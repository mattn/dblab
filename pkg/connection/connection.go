package connection

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"

	go_ora "github.com/sijms/go-ora/v2"

	"github.com/danvergara/dblab/pkg/command"
	"github.com/danvergara/dblab/pkg/drivers"
)

var (
	// pattern used to parse an incoming dsn for mysql connection.
	dsnPattern *regexp.Regexp
	// ErrCantDetectUSer is the error used to notify that a default username is not found
	// in the system to be used as database username.
	ErrCantDetectUSer = errors.New("could not detect default username")
	// ErrInvalidPostgresURLFormat is the error used to notify that the postgres given url is not valid.
	ErrInvalidPostgresURLFormat = errors.New(
		"invalid url - valid format: postgres://user:password@host:port/db?sslmode=mode",
	)
	// ErrInvalidMySQLURLFormat is the error used to notify that the given mysql url is not valid.
	ErrInvalidMySQLURLFormat = errors.New(
		"invalid url - valid format: mysql://user:password@tcp(host:port)/db",
	)
	ErrInvalidOracleURLFormat = errors.New(
		"invalid url - valid format: oracle://user:pass@server/service_name",
	)
	// ErrInvalidURLFormat is used to notify the url is invalid.
	ErrInvalidURLFormat = errors.New("invalid url")
	// ErrInvalidDriver is used to notify that the provided driver is not supported.
	ErrInvalidDriver = errors.New("invalid driver")
	// ErrInvalidSqlite3Extension is used to notify that the selected file is not a sqlite3 file.
	ErrInvalidSqlite3Extension = errors.New("invalid sqlite file extension")
	// ErrSocketFileDoNotExist indicates that the given path to the socket files leads to no file.
	ErrSocketFileDoNotExist = errors.New("socket file does not exist")
	// ErrInvalidSocketFile indicates that the socket file must end with .sock as suffix.
	ErrInvalidSocketFile = errors.New("invalid socket file - must end with .sock")
	// ErrInvalidOraclePort indicates that the port passed is not a proper integer.
	ErrInvalidOraclePort = errors.New("invalid oracle port")
)

func init() {
	dsnPattern = regexp.MustCompile(
		`^(?:(?P<user>.*?)(?::(?P<passwd>.*))?@)?` + // [user[:password]@]
			`(?:(?P<net>[^\(]*)(?:\((?P<addr>[^\)]*)\))?)?` + // [net[(addr)]]
			`\/(?P<dbname>.*?)` + // /dbname
			`(?:\?(?P<params>[^\?]*))?$`) // [?param1=value1&paramN=valueN]
}

// BuildConnectionFromOpts return the connection uri string given the options passed by the uses.
func BuildConnectionFromOpts(opts command.Options) (string, command.Options, error) {
	if opts.URL != "" {
		if strings.HasPrefix(opts.URL, drivers.Postgres) {
			opts.Driver = drivers.Postgres

			conn, err := formatPostgresURL(opts)

			return conn, opts, err
		}

		if strings.HasPrefix(opts.URL, drivers.MySQL) {
			opts.Driver = drivers.MySQL
			conn, err := formatMySQLURL(opts)
			return conn, opts, err
		}

		// this options is for sqlite.
		// For more information see https://github.com/mattn/go-sqlite3#connection-string.
		if strings.HasPrefix(opts.URL, "file:") {
			opts.Driver = drivers.Oracle
			return opts.URL, opts, nil
		}

		if strings.HasPrefix(opts.URL, "oracle:") {
			opts.Driver = drivers.Oracle
			conn, err := formatOracleURL(opts)
			return conn, opts, err
		}

		return "", opts, fmt.Errorf("%s: %w", opts.URL, ErrInvalidURLFormat)
	}

	if opts.User == "" {
		u, err := currentUser()
		if err == nil {
			opts.User = u
		}
	}

	switch opts.Driver {
	case drivers.Oracle:
		iPort, err := strconv.Atoi(opts.Port)
		if err != nil {
			return "", opts, fmt.Errorf("%v : %w", err, ErrInvalidPostgresURLFormat)
		}

		urloptions := make(map[string]string)

		if opts.SSL != "" {
			urloptions["SSL"] = opts.SSL
		}

		if opts.SSLVerify != "" {
			urloptions["SSL Verify"] = opts.SSLVerify
		}

		if opts.TraceFile != "" {
			urloptions["TRACE FILE"] = opts.TraceFile
		}

		if opts.Wallet != "" {
			urloptions["wallet"] = url.QueryEscape(opts.Wallet)
		}

		connStr := go_ora.BuildUrl(
			opts.Host,
			iPort,
			opts.DBName,
			opts.User,
			opts.Pass,
			urloptions,
		)

		return connStr, opts, nil
	case drivers.Postgres:
		query := url.Values{}
		if opts.Socket != "" {
			query.Add("host", opts.Socket)

			connDB := url.URL{
				Scheme:   opts.Driver,
				Path:     fmt.Sprintf("/%s", opts.DBName),
				RawQuery: query.Encode(),
			}

			switch {
			case opts.User != "" && opts.Pass == "":
				connDB.User = url.User(opts.User)
			case opts.User != "" && opts.Pass != "":
				connDB.User = url.UserPassword(opts.User, opts.Pass)
			}

			return connDB.String(), opts, nil
		}

		if opts.SSL != "" {
			query.Add("sslmode", opts.SSL)
		} else {
			if opts.Host == "localhost" || opts.Host == "127.0.0.1" {
				query.Add("sslmode", "disable")
			}
		}

		if opts.SSLCert != "" {
			query.Add("sslcert", opts.SSLCert)
		}

		if opts.SSLKey != "" {
			query.Add("sslkey", opts.SSLKey)
		}

		if opts.SSLPassword != "" {
			query.Add("sslpassword", opts.SSLPassword)
		}

		if opts.SSLRootcert != "" {
			query.Add("sslrootcert", opts.SSLRootcert)
		}

		connDB := url.URL{
			Scheme:   opts.Driver,
			Host:     fmt.Sprintf("%v:%v", opts.Host, opts.Port),
			User:     url.UserPassword(opts.User, opts.Pass),
			Path:     fmt.Sprintf("/%s", opts.DBName),
			RawQuery: query.Encode(),
		}

		return connDB.String(), opts, nil
	case drivers.MySQL:
		if opts.Socket != "" {
			if !validSocketFile(opts.Socket) {
				return "", opts, ErrInvalidSocketFile
			}

			if !socketFileExists(opts.Socket) {
				return "", opts, ErrSocketFileDoNotExist
			}

			return fmt.Sprintf(
				"%s:%s@unix(%s)/%s?charset=utf8",
				opts.User,
				opts.Pass,
				opts.Socket,
				opts.DBName,
			), opts, nil
		}

		return fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s",
			opts.User,
			opts.Pass,
			opts.Host,
			opts.Port,
			opts.DBName,
		), opts, nil
	case drivers.SQLite:
		if hasValidSqlite3FileExtension(opts.DBName) {
			return opts.DBName, opts, nil
		}

		return "", opts, fmt.Errorf("%s: %w", opts.URL, ErrInvalidSqlite3Extension)
	default:
		return "", opts, fmt.Errorf("%s: %w", opts.URL, ErrInvalidDriver)
	}
}

func currentUser() (string, error) {
	u, err := user.Current()
	if err == nil {
		return u.Username, nil
	}

	name := os.Getenv("USER")
	if name != "" {
		return name, nil
	}

	return "", nil
}

// formatPostgresURL returns valid uri for postgres connection.
func formatPostgresURL(opts command.Options) (string, error) {
	if !hasValidPostgresPrefix(opts.URL) {
		return "", fmt.Errorf("invalid prefix %s : %w", opts.URL, ErrInvalidPostgresURLFormat)
	}

	uri, err := url.Parse(opts.URL)
	if err != nil {
		return "", fmt.Errorf("%v : %w", err, ErrInvalidPostgresURLFormat)
	}

	result := map[string]string{}
	for k, v := range uri.Query() {
		result[strings.ToLower(k)] = v[0]
	}

	if result["sslmode"] == "" {
		if opts.SSL == "" {
			if strings.Contains(uri.Host, "localhost") || strings.Contains(uri.Host, "127.0.0.1") {
				result["sslmode"] = "disable"
			}
		} else {
			result["sslmode"] = opts.SSL
		}
	}

	query := url.Values{}
	for k, v := range result {
		query.Add(k, v)
	}
	uri.RawQuery = query.Encode()

	return uri.String(), nil
}

// formatMySQLURL returns valid uri for mysql connection.
func formatMySQLURL(opts command.Options) (string, error) {
	if !hasValidMySQLPrefix(opts.URL) {
		return "", fmt.Errorf("%s, %w", opts.URL, ErrInvalidMySQLURLFormat)
	}

	var e *url.Error

	// removes the mysql:// scheme since mysql does not need this.
	// we need it to know what database we're trying to connect to.
	opts.URL = strings.ReplaceAll(opts.URL, "mysql://", "")

	uri, err := url.Parse(opts.URL)
	if err != nil {
		// checks if *url.Error is the type of the error.
		// if the url is a dsn for mysql connection
		// the most likely is this is gonna be true.
		if errors.As(err, &e) {
			url, err := parseDSN(opts.URL)
			if err != nil {
				return "", fmt.Errorf("%v %w", err, ErrInvalidMySQLURLFormat)
			}

			return url, nil
		}

		return "", fmt.Errorf("%v %w", err, ErrInvalidMySQLURLFormat)
	}

	result := map[string]string{}
	for k, v := range uri.Query() {
		result[strings.ToLower(k)] = v[0]
	}

	query := url.Values{}
	for k, v := range result {
		query.Add(k, v)
	}
	uri.RawQuery = query.Encode()

	return uri.String(), nil
}

// formatOracleURL returns valid uri for oracle connection.
func formatOracleURL(opts command.Options) (string, error) {
	if !hasValidOraclePrefix(opts.URL) {
		return "", fmt.Errorf("invalid prefix %s : %w", opts.URL, ErrInvalidOracleURLFormat)
	}

	uri, err := url.Parse(opts.URL)
	if err != nil {
		return "", fmt.Errorf("%v : %w", err, ErrInvalidOracleURLFormat)
	}

	result := map[string]string{}
	for k, v := range uri.Query() {
		result[strings.ToLower(k)] = v[0]
	}

	query := url.Values{}
	for k, v := range result {
		query.Add(k, v)
	}
	uri.RawQuery = query.Encode()

	return uri.String(), nil
}

// validates if dsn pattern match with the parameter.
func parseDSN(dsn string) (string, error) {
	matches := dsnPattern.FindStringSubmatch(dsn)
	if matches == nil {
		return "", errors.New("not match")
	}

	names := dsnPattern.SubexpNames()
	if len(names) == 0 {
		return "", errors.New("not names")
	}

	return dsn, nil
}

// hasValidPostgresPrefix checks if a given url has the driver name in it.
func hasValidPostgresPrefix(rawurl string) bool {
	return strings.HasPrefix(rawurl, "postgres://") || strings.HasPrefix(rawurl, "postgresql://")
}

// hasValidMySQLPrefix checks if a given url has the driver name in it.
func hasValidMySQLPrefix(rawurl string) bool {
	return strings.HasPrefix(rawurl, "mysql://")
}

// hasValidOraclePrefix checks if a given url has the driver name in it.
func hasValidOraclePrefix(rawurl string) bool {
	return strings.HasPrefix(rawurl, "oracle://")
}

func hasValidSqlite3FileExtension(fileName string) bool {
	return strings.HasSuffix(fileName, "sqlite") || strings.HasSuffix(fileName, "db") ||
		strings.HasSuffix(fileName, "db3") ||
		strings.HasSuffix(fileName, "sqlite3")
}

func socketFileExists(socketFile string) bool {
	info, err := os.Stat(socketFile)
	if os.IsNotExist(err) {
		return false
	}

	return !info.IsDir()
}

func validSocketFile(socketFile string) bool {
	return strings.HasSuffix(socketFile, ".sock")
}
