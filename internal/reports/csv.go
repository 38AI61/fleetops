package reports

import (
	"database/sql"
	"encoding/csv"
	"io"
	"strconv"
)

// csvHeader is the GF-327 header row, byte for byte.
var csvHeader = []string{"date", "driver_id", "driver_name", "jobs_completed", "on_time_count",
	"on_time_denom", "on_time_pct", "distance_m", "idle_s"}

// flushEvery is the row count between explicit flushes, so a long export streams (GF-327).
const flushEvery = 100

// WriteCSV streams the rows of one day as CSV (GF-327). Rows are read from the
// cursor and written as they come, so the body is never held in memory. The
// stdlib writer does the quoting and CRLF line endings (RFC 4180). flush, when
// set, pushes the response out every flushEvery rows and at the end.
func WriteCSV(w io.Writer, date string, rows *sql.Rows, flush func()) error {
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	n := 0
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return err
		}
		if err := cw.Write(csvRecord(date, r)); err != nil {
			return err
		}
		n++
		if n%flushEvery == 0 {
			cw.Flush()
			if err := cw.Error(); err != nil {
				return err
			}
			if flush != nil {
				flush()
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	if flush != nil {
		flush()
	}
	return nil
}

// csvRecord formats one row. Numbers use the same shortest form as the JSON
// body, and on_time_pct is empty when it is null (GF-327).
func csvRecord(date string, r Row) []string {
	pct := ""
	if r.OnTimePct != nil {
		pct = strconv.FormatFloat(*r.OnTimePct, 'f', -1, 64)
	}
	return []string{
		date,
		strconv.FormatInt(r.DriverID, 10),
		r.DriverName,
		strconv.Itoa(r.JobsCompleted),
		strconv.Itoa(r.OnTimeCount),
		strconv.Itoa(r.OnTimeDenom),
		pct,
		strconv.FormatFloat(r.DistanceM, 'f', -1, 64),
		strconv.Itoa(r.IdleS),
	}
}
