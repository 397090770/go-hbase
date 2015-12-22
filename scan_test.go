package hbase

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"time"

	. "github.com/pingcap/check"
)

type ScanTestSuit struct {
	cli       HBaseClient
	tableName string
}

var _ = Suite(&ScanTestSuit{})

func (s *ScanTestSuit) SetUpTest(c *C) {
	var (
		err error
	)

	s.cli, err = NewClient(getTestZkHosts(), "/hbase")
	c.Assert(err, IsNil)

	s.tableName = "test_scan"
	table := NewTableNameWithDefaultNS(s.tableName)
	tblDesc := NewTableDesciptor(table)
	cf := newColumnFamilyDescriptor("cf", 3)
	tblDesc.AddColumnDesc(cf)
	s.cli.CreateTable(tblDesc, nil)
}

func (s *ScanTestSuit) TearDownTest(c *C) {
	err := s.cli.DisableTable(NewTableNameWithDefaultNS(s.tableName))
	c.Assert(err, IsNil)

	err = s.cli.DropTable(NewTableNameWithDefaultNS(s.tableName))
	c.Assert(err, IsNil)
}

func (s *ScanTestSuit) TestNextKey(c *C) {
	inputs := []struct {
		data   []byte
		result []byte
	}{
		{nil, []byte{0}},
		{[]byte{}, []byte{0}},
		{[]byte{0}, []byte{1}},
		{[]byte{1, 2, 3}, []byte{1, 2, 4}},
		{[]byte{1, 255}, []byte{2, 0}},
		{[]byte{255}, []byte{0, 0}},
		{[]byte{255, 255}, []byte{0, 0, 0}},
	}

	for _, input := range inputs {
		result := nextKey(input.data)
		c.Assert(result, BytesEquals, input.result, Commentf("%v - %v - %v", input.data, input.result, result))
	}
}

func (s *ScanTestSuit) TestScan(c *C) {
	for i := 1; i <= 10000; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("qscan"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	runtime.GOMAXPROCS(runtime.NumCPU())
	wg := sync.WaitGroup{}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scan := NewScan([]byte(s.tableName), 100, s.cli)
			defer scan.Close()
			// [1, 5)
			scan.StartRow = []byte("1")
			scan.StopRow = []byte("5")

			cnt := 0
			for {
				r := scan.Next()
				if r == nil || scan.Closed() {
					break
				}
				cnt++
			}
			c.Assert(cnt, Equals, 4445)
		}()
	}

	wg.Wait()

	for i := 1; i <= 10000; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestScanWithVersion(c *C) {
	for i := 1; i <= 10000; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("q"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	// Test version put.
	p := NewPut([]byte("99999"))
	p.AddValue([]byte("cf"), []byte("q"), []byte("1002"))
	p.AddTimestamp(1002)
	ok, err := s.cli.Put(s.tableName, p)
	c.Assert(ok, IsTrue)
	c.Assert(err, IsNil)

	p = NewPut([]byte("99999"))
	p.AddValue([]byte("cf"), []byte("q"), []byte("1003"))
	p.AddTimestamp(1003)
	ok, err = s.cli.Put(s.tableName, p)
	c.Assert(ok, IsTrue)
	c.Assert(err, IsNil)

	p = NewPut([]byte("99999"))
	p.AddValue([]byte("cf"), []byte("q"), []byte("1001"))
	p.AddTimestamp(1001)
	ok, err = s.cli.Put(s.tableName, p)
	c.Assert(ok, IsTrue)
	c.Assert(err, IsNil)

	// Test version get.
	g := NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	g.AddTimeRange(1001, 1004)
	r, err := s.cli.Get(s.tableName, g)
	c.Assert(r.Columns["cf:q"].Values, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "1003")
	c.Assert(err, IsNil)

	g = NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	g.AddTimeRange(1001, 1003)
	r, err = s.cli.Get(s.tableName, g)
	c.Assert(r.Columns["cf:q"].Values, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "1002")
	c.Assert(err, IsNil)

	g = NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	g.AddTimeRange(999, 1001)
	r, err = s.cli.Get(s.tableName, g)
	c.Assert(r, IsNil)
	c.Assert(err, IsNil)

	g = NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	r, err = s.cli.Get(s.tableName, g)
	c.Assert(err, IsNil)
	c.Assert(r.Columns["cf:q"].Values, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "1003")
	c.Assert(r.SortedColumns[0].Ts, Equals, uint64(1003))

	g = NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	g.Versions = 2
	r, err = s.cli.Get(s.tableName, g)
	c.Assert(r.Columns["cf:q"].Values, HasLen, 2)
	value, ok := r.Columns["cf:q"].Values[1003]
	c.Assert(ok, IsTrue)
	c.Assert(string(value), Equals, "1003")
	value, ok = r.Columns["cf:q"].Values[1002]
	c.Assert(ok, IsTrue)
	c.Assert(string(value), Equals, "1002")

	g = NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	g.Versions = 5
	r, err = s.cli.Get(s.tableName, g)
	c.Assert(r.Columns["cf:q"].Values, HasLen, 3)
	value, ok = r.Columns["cf:q"].Values[1003]
	c.Assert(ok, IsTrue)
	c.Assert(string(value), Equals, "1003")
	value, ok = r.Columns["cf:q"].Values[1002]
	c.Assert(ok, IsTrue)
	c.Assert(string(value), Equals, "1002")
	value, ok = r.Columns["cf:q"].Values[1001]
	c.Assert(ok, IsTrue)
	c.Assert(string(value), Equals, "1001")

	g = NewGet([]byte("99999"))
	g.AddColumn([]byte("cf"), []byte("q"))
	g.Versions = 3
	g.AddTimeRange(999, 1002)
	r, err = s.cli.Get(s.tableName, g)
	c.Assert(r.Columns["cf:q"].Values, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "1001")
	c.Assert(r.SortedColumns[0].Ts, Equals, uint64(1001))

	// Test version scan.
	// Range -> [9999, 999999)
	scan := NewScan([]byte(s.tableName), 100, s.cli)
	scan.StartRow = []byte("9999")
	scan.StopRow = []byte("999999")

	r = scan.Next()
	c.Assert(r.SortedColumns, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "9999")

	r = scan.Next()
	c.Assert(r.SortedColumns, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "1003")

	r = scan.Next()
	c.Assert(r, IsNil)
	scan.Close()

	// Range -> [9999, 999999)
	// Version -> [1000, 1002)
	scan = NewScan([]byte(s.tableName), 100, s.cli)
	scan.StartRow = []byte("9999")
	scan.StopRow = []byte("999999")
	scan.AddTimeRange(1000, 1002)

	r = scan.Next()
	c.Assert(r.SortedColumns, HasLen, 1)
	c.Assert(string(r.SortedColumns[0].Value), Equals, "1001")

	r = scan.Next()
	c.Assert(r, IsNil)
	scan.Close()

	for i := 1; i <= 10000; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestScanCrossMultiRegions(c *C) {
	origData := map[string]map[string]string{}
	for i := 1; i <= 2000; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("qmulti"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)

		rowKey := string(p.Row)
		origData[rowKey] = map[string]string{}
		origData[rowKey]["cf:qmulti"] = string(p.Values[0][0])
	}
	err := s.cli.Split(s.tableName, "1024")
	c.Assert(err, IsNil)
	// Sleep wait Split finish.
	time.Sleep(1 * time.Second)

	scan := NewScan([]byte(s.tableName), 100, s.cli)
	cnt := 0
	for i := 1; i <= 2000; i++ {
		r := scan.Next()
		c.Assert(r, NotNil)
		rowKey := string(r.Row)
		origRow, ok := origData[rowKey]
		c.Assert(ok, IsTrue)
		c.Assert(origRow, NotNil)
		for column := range r.Columns {
			origValue, ok := origRow[column]
			c.Assert(ok, IsTrue)
			value := string(r.Columns[column].Value)
			c.Assert(origValue, Equals, value)
		}
		delete(origData, rowKey)
		cnt++
	}
	c.Assert(cnt, Equals, 2000)

	for i := 1; i <= 2000; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestScanWhileSplit(c *C) {
	origData := map[string]map[string]string{}
	for i := 1; i <= 3000; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("qsplit"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)

		rowKey := string(p.Row)
		origData[rowKey] = map[string]string{}
		origData[rowKey]["cf:qsplit"] = string(p.Values[0][0])
	}
	scan := NewScan([]byte(s.tableName), 100, s.cli)
	cnt := 0
	// Scan to a random row, like 1987 or whatever.
	for i := 1; i <= 1987; i++ {
		r := scan.Next()
		c.Assert(r, NotNil)
		rowKey := string(r.Row)
		origRow, ok := origData[rowKey]
		c.Assert(ok, IsTrue)
		c.Assert(origRow, NotNil)
		for column := range r.Columns {
			origValue, ok := origRow[column]
			c.Assert(ok, IsTrue)
			value := string(r.Columns[column].Value)
			c.Assert(origValue, Equals, value)
		}
		delete(origData, rowKey)
		cnt++
	}
	c.Assert(cnt, Equals, 1987)
	// At this time, regions is splitting.
	err := s.cli.Split(s.tableName, "2048")
	c.Assert(err, IsNil)
	// Sleep wait Split finish.
	time.Sleep(1 * time.Second)

	// Scan is go on, and get data normally.
	for i := 1988; i <= 3000; i++ {
		r := scan.Next()
		c.Assert(r, NotNil)
		rowKey := string(r.Row)
		origRow, ok := origData[rowKey]
		c.Assert(ok, IsTrue)
		c.Assert(origRow, NotNil)
		for column := range r.Columns {
			origValue, ok := origRow[column]
			c.Assert(ok, IsTrue)
			value := string(r.Columns[column].Value)
			c.Assert(origValue, Equals, value)
		}
		for column := range r.Columns {
			origValue, ok := origRow[column]
			value := string(r.Columns[column].Value)
			c.Assert(ok, IsTrue)
			c.Assert(origValue, Equals, value)
		}
		delete(origData, rowKey)
		cnt++
	}
	c.Assert(cnt, Equals, 3000)

	for i := 1; i <= 3000; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestScanClose(c *C) {
	for i := 1; i <= 100; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("qclose"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	// Do not get any data.
	scan := NewScan([]byte(s.tableName), 100, s.cli)
	err := scan.Close()
	c.Assert(err, IsNil)
	r := scan.Next()
	c.Assert(r, IsNil)

	// Get some data.
	scan = NewScan([]byte(s.tableName), 100, s.cli)
	r = scan.Next()
	c.Assert(r, NotNil)
	// If scanner Close, then do not fetch any data even cache still has some.
	err = scan.Close()
	c.Assert(err, IsNil)
	r = scan.Next()
	c.Assert(r, IsNil)

	for i := 1; i <= 100; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestInRange(c *C) {
	// nil < x < y < z
	x := []byte("1")
	y := []byte("13")
	z := []byte("42")
	c.Assert(inRange(x, y, z), IsFalse)
	c.Assert(inRange(x, z, y), IsFalse)
	c.Assert(inRange(y, x, z), IsTrue)
	c.Assert(inRange(y, z, x), IsFalse)
	c.Assert(inRange(z, x, y), IsFalse)
	c.Assert(inRange(z, y, x), IsFalse)

	c.Assert(inRange(y, nil, z), IsTrue)
	c.Assert(inRange(y, x, nil), IsFalse)
	c.Assert(inRange(nil, x, z), IsFalse)
}

func (s *ScanTestSuit) TestScanDataSorted(c *C) {
	for i := 1; i <= 10000; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("qsorted"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	// batchSize is not a divisor of 10000 for corner case
	scan := NewScan([]byte(s.tableName), 101, s.cli)
	last := scan.Next()
	for i := 2; i <= 10000; i++ {
		cur := scan.Next()
		c.Assert(last.Row, Less, cur.Row)
	}

	for i := 1; i <= 10000; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestSeekNext(c *C) {
	// Insert data "001" to "300".
	for i := 1; i <= 300; i++ {
		p := NewPut([]byte(fmt.Sprintf("%03d", i)))
		p.AddValue([]byte("cf"), []byte("qseekgot"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	sscan := NewSeekScan([]byte(s.tableName), 101, s.cli)

	// First nextBatch().
	for i := 1; i <= 101; i++ {
		resRow := sscan.Next()
		c.Assert(sscan.cacheStartRow, BytesEquals, []byte("001"))
		c.Assert(sscan.cacheStopRow, BytesEquals, []byte("101"))
		c.Assert(sscan.cachePos, Equals, i)
		c.Assert(resRow.Row, BytesEquals, []byte(fmt.Sprintf("%03d", i)))
	}
	// Second nextBatch().
	for i := 102; i <= 202; i++ {
		resRow := sscan.Next()
		c.Assert(sscan.cacheStartRow, BytesEquals, []byte("102"))
		c.Assert(sscan.cacheStopRow, BytesEquals, []byte("202"))
		c.Assert(sscan.cachePos, Equals, i-101)
		c.Assert(resRow.Row, BytesEquals, []byte(fmt.Sprintf("%03d", i)))
	}
	// Third nextBatch().
	for i := 203; i <= 300; i++ {
		resRow := sscan.Next()
		c.Assert(sscan.cachePos, Equals, i-202)
		c.Assert(resRow.Row, BytesEquals, []byte(fmt.Sprintf("%03d", i)))
	}
	// No more.
	resRow := sscan.Next()
	c.Assert(resRow, IsNil)

	// Next() after Close().
	sscan = NewSeekScan([]byte(s.tableName), 101, s.cli)
	err := sscan.Close()
	c.Assert(err, IsNil)
	resRow = sscan.Next()
	c.Assert(resRow, IsNil)

	for i := 1; i <= 300; i++ {
		d := NewDelete([]byte(fmt.Sprintf("%03d", i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestSeekGot(c *C) {
	// Insert data "1" to "1000".
	for i := 1; i <= 1000; i++ {
		p := NewPut([]byte(strconv.Itoa(i)))
		p.AddValue([]byte("cf"), []byte("qseekgot"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	sscan := NewSeekScan([]byte(s.tableName), 101, s.cli)

	// Be found easily.
	resRow, err := sscan.Seek([]byte("2"))
	c.Assert(err, IsNil)
	c.Assert(resRow, NotNil)
	c.Assert(resRow.Row, BytesEquals, []byte("2"))
	scanId := sscan.id
	lastStartRow := sscan.cacheStartRow
	lastStopRow := sscan.cacheStopRow
	c.Assert(
		inRange([]byte("201"), sscan.cacheStartRow, sscan.cacheStopRow),
		IsTrue)

	// Row == "2" or "201" are both in same cache.
	resRow, err = sscan.Seek([]byte("201"))
	c.Assert(err, IsNil)
	c.Assert(resRow, NotNil)
	c.Assert(resRow.Row, BytesEquals, []byte("201"))
	// scanId, cacheStartRow, cacheStopRow are all equal means don't trigger nextBatch().
	c.Assert(scanId, Equals, sscan.id)
	c.Assert(lastStartRow, BytesEquals, sscan.cacheStartRow)
	c.Assert(lastStopRow, BytesEquals, sscan.cacheStopRow)

	// Use Next() iterator to row == "201", and try Seek() again.
	nextRow := sscan.Next()
	for bytes.Compare(nextRow.Row, []byte("201")) < 0 {
		nextRow = sscan.Next()
	}
	c.Assert(nextRow.Row, BytesEquals, []byte("201"))
	resRow, err = sscan.Seek([]byte("2"))
	c.Assert(err, IsNil)
	c.Assert(resRow, NotNil)
	c.Assert(resRow.Row, BytesEquals, []byte("2"))
	c.Assert(scanId, Equals, sscan.id)
	c.Assert(lastStartRow, BytesEquals, sscan.cacheStartRow)
	c.Assert(lastStopRow, BytesEquals, sscan.cacheStopRow)

	// Seek forward, need invoke nextbatch().
	resRow, err = sscan.Seek([]byte("1"))
	c.Assert(err, IsNil)
	c.Assert(resRow, NotNil)
	c.Assert(resRow.Row, BytesEquals, []byte("1"))
	// Scan must be a new scan.
	c.Assert(scanId, Not(Equals), sscan.id)
	c.Assert(lastStartRow, Not(BytesEquals), sscan.cacheStartRow)
	c.Assert(lastStopRow, Not(BytesEquals), sscan.cacheStopRow)

	// Seek forward, need invoke nextbatch() and return first >= "0" which is "1".
	resRow, err = sscan.Seek([]byte("0"))
	c.Assert(err, IsNil)
	c.Assert(resRow, NotNil)
	c.Assert(resRow.Row, BytesEquals, []byte("1"))
	// Scan must be a new scan.
	c.Assert(scanId, Not(Equals), sscan.id)
	c.Assert(lastStartRow, Not(BytesEquals), sscan.cacheStartRow)
	c.Assert(lastStopRow, Not(BytesEquals), sscan.cacheStopRow)

	for i := 1; i <= 1000; i++ {
		d := NewDelete([]byte(strconv.Itoa(i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}

func (s *ScanTestSuit) TestSeekFailed(c *C) {
	// Insert data "001" to "300".
	for i := 1; i <= 300; i++ {
		p := NewPut([]byte(fmt.Sprintf("%03d", i)))
		p.AddValue([]byte("cf"), []byte("qseekfailed"), []byte(strconv.Itoa(i)))
		ok, err := s.cli.Put(s.tableName, p)
		c.Assert(ok, IsTrue)
		c.Assert(err, IsNil)
	}
	sscan := NewSeekScan([]byte(s.tableName), 101, s.cli)

	// Not found.
	resRow, err := sscan.Seek([]byte("4"))
	c.Assert(err, NotNil)
	c.Assert(resRow, IsNil)

	err = sscan.Close()
	c.Assert(err, IsNil)
	resRow, err = sscan.Seek([]byte("001"))
	c.Assert(err, NotNil)
	c.Assert(resRow, IsNil)

	for i := 1; i <= 300; i++ {
		d := NewDelete([]byte(fmt.Sprintf("%03d", i)))
		b, err := s.cli.Delete(s.tableName, d)
		c.Assert(err, IsNil)
		c.Assert(b, IsTrue)
	}
}
