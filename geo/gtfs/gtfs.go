package gtfs

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func Parse(gtfsZipFile, csvFile string, columns []string, f func([]string) error) error {
	z, err := zip.OpenReader(gtfsZipFile)
	if err != nil {
		return err
	}
	defer z.Close()
	zf, err := z.Open(csvFile)
	if err != nil {
		return err
	}
	defer zf.Close()
	c := csv.NewReader(zf)
	c.ReuseRecord, c.Comment = true, '#'
	header, err := c.Read()
	if err != nil {
		return err
	}
	row, indexes := make([]string, len(columns)), make([]int, len(columns))
	for i := range indexes {
		indexes[i] = -1
	}
	for i, hc := range header {
		for j, c := range columns {
			if c == hc {
				columns[j], indexes[j] = "", i
			}
		}
	}
	for {
		record, err := c.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		for i, j := range indexes {
			row[i] = record[j]
		}
		if err := f(row); err != nil {
			return err
		}
	}
	return nil
}

func ParseClockTime(hhmmss string) (sinceMidnight time.Duration, err error) {
	i := strings.IndexRune(hhmmss, ':') // hh:mm:ss OR h:mm:ss
	if i == -1 {
		return 0, fmt.Errorf("malformed hhmmss string: %s", hhmmss)
	}
	hh, err := strconv.Atoi(hhmmss[:i])
	if err != nil {
		return 0, err
	}
	mm, err := strconv.Atoi(hhmmss[i+1 : i+3])
	if err != nil {
		return 0, err
	}
	ss, err := strconv.Atoi(hhmmss[i+4 : i+6])
	if err != nil {
		return 0, err
	}
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute + time.Duration(ss)*time.Second, nil
}
