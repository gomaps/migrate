// Package postgres implements the Driver interface.
package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"os/user"
	"strconv"

	"github.com/gomaps/migrate/file"
	"github.com/gomaps/migrate/migrate/direction"
	"github.com/lib/pq"
)

type Driver struct {
	db *sql.DB
}

const tableName = "schema_version"

func (driver *Driver) Initialize(url string) error {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return err
	}
	if err := db.Ping(); err != nil {
		return err
	}
	driver.db = db

	if err := driver.ensureVersionTableExists(); err != nil {
		return err
	}
	return nil
}

func (driver *Driver) Close() error {
	if err := driver.db.Close(); err != nil {
		return err
	}
	return nil
}

func (driver *Driver) ensureVersionTableExists() error {
	if _, err := driver.db.Exec("CREATE TABLE IF NOT EXISTS " + tableName + `
	(
		version int not null primary key,
		version_rank int, 
		installed_rank int,
		description varchar(500),
		type varchar(500),
		script varchar(500),
		checksum  int,
		installed_by varchar(500),
		execution_time int,
		success boolean
	);`); err != nil {
		return err
	}
	return nil
}

func (driver *Driver) FilenameExtension() string {
	return "sql"
}

func (driver *Driver) Migrate(f file.File, pipe chan interface{}) {
	defer close(pipe)
	pipe <- f

	tx, err := driver.db.Begin()
	if err != nil {
		pipe <- err
		return
	}

	// Read content along with calculating checksum
	if err := f.ReadContent(); err != nil {
		pipe <- err
		return
	}

	if f.Direction == direction.Up {
		q := "INSERT INTO " + tableName
		q += " (version, version_rank, installed_rank, description, type, script, checksum, installed_by, execution_time, success)"
		q += " VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)"
		user, err := user.Current()
		if err != nil {
			pipe <- err
			return
		}
		if _, err := tx.Exec(q, f.Version, f.Rank, f.Rank, f.Name, "SQL", f.FileName, f.Checksum, user.Name, 0, true); err != nil {
			pipe <- err
			if err := tx.Rollback(); err != nil {
				pipe <- err
			}
			return
		}
	} else if f.Direction == direction.Down {
		if _, err := tx.Exec("DELETE FROM "+tableName+" WHERE version=$1", f.Version); err != nil {
			pipe <- err
			if err := tx.Rollback(); err != nil {
				pipe <- err
			}
			return
		}
	}

	if _, err := tx.Exec(string(f.Content)); err != nil {
		pqErr := err.(*pq.Error)
		offset, err := strconv.Atoi(pqErr.Position)
		if err == nil && offset >= 0 {
			lineNo, columnNo := file.LineColumnFromOffset(f.Content, offset-1)
			errorPart := file.LinesBeforeAndAfter(f.Content, lineNo, 5, 5, true)
			pipe <- errors.New(fmt.Sprintf("%s %v: %s in line %v, column %v:\n\n%s", pqErr.Severity, pqErr.Code, pqErr.Message, lineNo, columnNo, string(errorPart)))
		} else {
			pipe <- errors.New(fmt.Sprintf("%s %v: %s", pqErr.Severity, pqErr.Code, pqErr.Message))
		}

		if err := tx.Rollback(); err != nil {
			pipe <- err
		}
		return
	}

	if err := tx.Commit(); err != nil {
		pipe <- err
		return
	}
}

func (driver *Driver) Version() (int, error) {
	var version int
	err := driver.db.QueryRow("SELECT version_rank FROM " + tableName + " ORDER BY version_rank DESC LIMIT 1").Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		return -1, nil
	case err != nil:
		return 0, err
	default:
		return version - 1, nil
	}
}
