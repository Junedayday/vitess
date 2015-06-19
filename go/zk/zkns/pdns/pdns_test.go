package pdns

import (
	"io"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/youtube/vitess/go/netutil"
	"github.com/youtube/vitess/go/zk"
	"launchpad.net/gozk/zookeeper"
)

const (
	fakeSRV = `{
"Entries": [
  {
    "host": "test1",
    "named_port_map": {"_http":8080}
  },
  {
    "host": "test2",
    "named_port_map": {"_http":8080}
  }
]}`

	fakeCNAME = `{
"Entries": [
  {
    "host": "test1"
  }
]}`

	fakeA = `{
"Entries": [
  {
    "ipv4": "0.0.0.1"
  }
]}`
)

var fqdn = netutil.FullyQualifiedHostnameOrPanic()

var zconn = &TestZkConn{map[string]string{
	"/zk/test/zkns/srv":   fakeSRV,
	"/zk/test/zkns/cname": fakeCNAME,
	"/zk/test/zkns/a":     fakeA,
}}

var queries = []string{
	"Q\t_http.srv.zkns.test.zk\tIN\tANY\t-1\t1.1.1.1\t1.1.1.2",
	"Q\ta.zkns.test.zk\tIN\tANY\t-1\t1.1.1.1\t1.1.1.2",
	"Q\tcname.zkns.test.zk\tIN\tANY\t-1\t1.1.1.1\t1.1.1.2",
	"Q\tempty.zkns.test.zk\tIN\tANY\t-1\t1.1.1.1\t1.1.1.2",
	// Sadly this test case generates a log error that cannot be squelched easily.
	"Q\tbad.domain.test.ignore.console.log.errors\tIN\tANY\t-1\t1.1.1.1\t1.1.1.2",
}

var testSOA = "DATA\t.zkns.test.zk.\tIN\tSOA\t1\t1\t" + fqdn + ". hostmaster." + fqdn + ". 0 1800 600 3600 300\n"
var results = []string{
	"OK\tzkns2pdns\n" + testSOA + "DATA\t_http.srv.zkns.test.zk\tIN\tSRV\t1\t1\t0\t0 8080 test1\nDATA\t_http.srv.zkns.test.zk\tIN\tSRV\t1\t1\t0\t0 8080 test2\nEND\n",
	"OK\tzkns2pdns\n" + testSOA + "DATA\ta.zkns.test.zk\tIN\tA\t1\t1\t0.0.0.1\nEND\n",
	"OK\tzkns2pdns\n" + testSOA + "DATA\tcname.zkns.test.zk\tIN\tCNAME\t1\t1\ttest1\nEND\n",
	"OK\tzkns2pdns\n" + testSOA + "END\n",
	"OK\tzkns2pdns\nFAIL\n",
}

func testQuery(t *testing.T, query, result string) {
	inpr, inpw, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	defer inpr.Close()
	outpr, outpw, err := os.Pipe()
	if err != nil {
		inpw.Close()
		panic(err)
	}
	defer outpr.Close()

	// It seems the outpw.Close() below (in the go routine) triggers EOF
	// on outpr before the FD is totally closed. Then in turn the defer
	// outpr.Close can hit early, and cause data integrity issues.
	// So using a synchronization channel to make sure the Close()
	// fully returns before we exit this method.
	sync := make(chan struct{})

	zr1 := newZknsResolver(zconn, fqdn, ".zkns.test.zk", "/zk/test/zkns")
	pd := &pdns{zr1}
	go func() {
		pd.Serve(inpr, outpw)
		outpw.Close()
		close(sync)
	}()

	_, err = io.WriteString(inpw, "HELO\t2\n")
	if err != nil {
		inpw.Close()
		t.Fatalf("write failed: %v", err)
	}
	_, err = io.WriteString(inpw, query)
	if err != nil {
		inpw.Close()
		t.Fatalf("write failed: %v", err)
	}

	inpw.Close()
	data, err := ioutil.ReadAll(outpr)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	qresult := string(data)
	if qresult != result {
		t.Fatalf("data mismatch found for %#v:\n%#v\nexpected:\n%#v", query, qresult, result)
	}
	<-sync
}

func TestQueries(t *testing.T) {
	for i, q := range queries {
		testQuery(t, q, results[i])
	}
}

// FIXME(msolomon) move to zk/fake package
type TestZkConn struct {
	data map[string]string
}

type ZkStat struct {
	czxid          int64     `bson:"Czxid"`
	mzxid          int64     `bson:"Mzxid"`
	cTime          time.Time `bson:"CTime"`
	mTime          time.Time `bson:"MTime"`
	version        int       `bson:"Version"`
	cVersion       int       `bson:"CVersion"`
	aVersion       int       `bson:"AVersion"`
	ephemeralOwner int64     `bson:"EphemeralOwner"`
	dataLength     int       `bson:"DataLength"`
	numChildren    int       `bson:"NumChildren"`
	pzxid          int64     `bson:"Pzxid"`
}

type ZkPath struct {
	Path string
}

type ZkPathV struct {
	Paths []string
}

type ZkNode struct {
	Path     string
	Data     string
	Stat     ZkStat
	Children []string
}

type ZkNodeV struct {
	Nodes []*ZkNode
}

// ZkStat methods to match zk.Stat interface
func (zkStat *ZkStat) Czxid() int64 {
	return zkStat.czxid
}

func (zkStat *ZkStat) Mzxid() int64 {
	return zkStat.mzxid
}

func (zkStat *ZkStat) CTime() time.Time {
	return zkStat.cTime
}

func (zkStat *ZkStat) MTime() time.Time {
	return zkStat.mTime
}

func (zkStat *ZkStat) Version() int {
	return zkStat.version
}

func (zkStat *ZkStat) CVersion() int {
	return zkStat.cVersion
}

func (zkStat *ZkStat) AVersion() int {
	return zkStat.aVersion
}

func (zkStat *ZkStat) EphemeralOwner() int64 {
	return zkStat.ephemeralOwner
}

func (zkStat *ZkStat) DataLength() int {
	return zkStat.dataLength
}

func (zkStat *ZkStat) NumChildren() int {
	return zkStat.numChildren
}

func (zkStat *ZkStat) Pzxid() int64 {
	return zkStat.pzxid
}

func (conn *TestZkConn) Get(path string) (data string, stat zk.Stat, err error) {
	data, ok := conn.data[path]
	if !ok {
		err = &zookeeper.Error{Op: "TestZkConn: node doesn't exist", Code: zookeeper.ZNONODE, Path: path}
		return
	}
	s := &ZkStat{}
	return data, s, nil
}

func (conn *TestZkConn) GetW(path string) (data string, stat zk.Stat, watch <-chan zookeeper.Event, err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) Children(path string) (children []string, stat zk.Stat, err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) ChildrenW(path string) (children []string, stat zk.Stat, watch <-chan zookeeper.Event, err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) Exists(path string) (stat zk.Stat, err error) {
	_, ok := conn.data[path]
	if ok {
		return &ZkStat{}, nil
	}
	return nil, nil
}

func (conn *TestZkConn) ExistsW(path string) (stat zk.Stat, watch <-chan zookeeper.Event, err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) Create(path, value string, flags int, aclv []zookeeper.ACL) (pathCreated string, err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) Set(path, value string, version int) (stat zk.Stat, err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) Delete(path string, version int) (err error) {
	panic("Should not be used")
}

func (conn *TestZkConn) Close() error {
	panic("Should not be used")
}

func (conn *TestZkConn) RetryChange(path string, flags int, acl []zookeeper.ACL, changeFunc zk.ChangeFunc) error {
	panic("Should not be used")
}

func (conn *TestZkConn) ACL(path string) ([]zookeeper.ACL, zk.Stat, error) {
	panic("Should not be used")
}

func (conn *TestZkConn) SetACL(path string, aclv []zookeeper.ACL, version int) error {
	panic("Should not be used")
}
