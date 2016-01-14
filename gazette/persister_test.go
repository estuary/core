package gazette

import (
	"io/ioutil"
	"os"
	"time"

	"github.com/coreos/go-etcd/etcd"
	gc "github.com/go-check/check"
	"github.com/stretchr/testify/mock"

	"github.com/pippio/api-server/cloudstore"
	"github.com/pippio/api-server/discovery"
	"github.com/pippio/gazette/journal"
)

type PersisterSuite struct {
	etcd      *discovery.EtcdMemoryService
	cfs       cloudstore.FileSystem
	file      *journal.MockFragmentFile
	fragment  journal.Fragment
	persister *Persister
}

func (s *PersisterSuite) SetUpTest(c *gc.C) {
	s.etcd = discovery.NewEtcdMemoryService()
	s.etcd.MakeDirectory(PersisterLocksRoot)

	s.cfs = cloudstore.NewTmpFileSystem()
	s.file = &journal.MockFragmentFile{}
	s.fragment = journal.Fragment{
		Journal: "a/journal",
		Begin:   1000,
		End:     1010,
		Sum: [...]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
			11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
		File: s.file,
	}
	s.persister = NewPersister("base/directory", s.cfs, s.etcd, "route-key")
}

func (s *PersisterSuite) TearDownTest(c *gc.C) {
	c.Check(s.cfs.Close(), gc.IsNil)
}

func (s *PersisterSuite) TestPersistence(c *gc.C) {
	kContentFixture := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}

	// Monitor persister locks. Expect a lock to be obtained, refreshed,
	// and then released.
	subscriber := &discovery.MockEtcdSubscriber{}
	s.expectLockUnlock(subscriber, c)
	blockUntilRefresh := make(chan time.Time)

	subscriber.On("OnEtcdUpdate", mock.MatchedBy(func(r *etcd.Response) bool {
		if r.Action != discovery.EtcdUpdateOp {
			return false
		}
		c.Check(r.Node.Key, gc.Equals, PersisterLocksRoot+s.fragment.ContentName())

		if blockUntilRefresh != nil {
			close(blockUntilRefresh)
			blockUntilRefresh = nil
		}
		return true
	}), mock.AnythingOfType("*etcd.Node"))

	c.Check(s.etcd.Subscribe(PersisterLocksRoot, subscriber), gc.IsNil)

	// Expect fragment.File to be read. Return a value fixture we'll verify later.
	s.file.On("ReadAt", mock.AnythingOfType("[]uint8"), int64(0)).
		Return(10, nil).
		WaitUntil(blockUntilRefresh). // Delay until a lock refresh occurrs.
		Run(func(args mock.Arguments) {
		copy(args.Get(0).([]byte), kContentFixture)
	}).Once()

	// Intercept and validate the call to os.Remove.
	s.persister.osRemove = func(path string) error {
		c.Check(path, gc.Equals, "base/directory/a/journal/"+s.fragment.ContentName())
		s.persister.osRemove = nil // Mark we were called.
		return nil
	}

	s.persister.persisterLockTTL = time.Millisecond
	s.persister.convergeOne(s.fragment)

	subscriber.AssertExpectations(c)
	s.file.AssertExpectations(c)
	c.Check(s.persister.osRemove, gc.IsNil)

	// Verify written content.
	r, err := s.cfs.Open(s.fragment.ContentPath())
	c.Check(err, gc.IsNil)
	content, _ := ioutil.ReadAll(r)
	c.Check(content, gc.DeepEquals, kContentFixture)
}

func (s *PersisterSuite) TestLockIsAlreadyHeld(c *gc.C) {
	s.etcd.Create(PersisterLocksRoot+s.fragment.ContentName(), "another-broker", 0)

	// Expect that no persister lock changes are made.
	subscriber := &discovery.MockEtcdSubscriber{}
	subscriber.On("OnEtcdUpdate", mock.Anything, mock.Anything).Return().Once()
	c.Check(s.etcd.Subscribe(PersisterLocksRoot, subscriber), gc.IsNil)

	// Note we're implicitly verifying that the local file is not read,
	// by not setting up expectations.

	// Also expect the local file is left alone.
	s.persister.osRemove = func(string) error {
		c.Log("os.Remove() called")
		c.Fail()
		return nil
	}
	s.persister.convergeOne(s.fragment)

	subscriber.AssertExpectations(c)
	s.file.AssertExpectations(c)

	// Expect it's not present on target filesystem.
	_, err := s.cfs.Open(s.fragment.ContentPath())
	c.Check(os.IsNotExist(err), gc.Equals, true)
}

func (s *PersisterSuite) TestTargetFileAlreadyExists(c *gc.C) {
	{
		c.Assert(s.cfs.MkdirAll(s.fragment.Journal.String(), 0740), gc.IsNil)
		w, err := s.cfs.OpenFile(s.fragment.ContentPath(),
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
		c.Check(err, gc.IsNil)
		w.Write([]byte("previous-content"))
		c.Assert(w.Close(), gc.IsNil)
	}

	// Expect a lock to be obtained, and then released.
	subscriber := &discovery.MockEtcdSubscriber{}
	s.expectLockUnlock(subscriber, c)
	c.Check(s.etcd.Subscribe(PersisterLocksRoot, subscriber), gc.IsNil)

	// Expect fragment.File is *not* read, but that the file *is* removed.
	s.persister.osRemove = func(path string) error {
		c.Check(path, gc.Equals, "base/directory/a/journal/"+s.fragment.ContentName())
		s.persister.osRemove = nil // Mark we were called.
		return nil
	}
	s.persister.convergeOne(s.fragment)

	subscriber.AssertExpectations(c)
	s.file.AssertExpectations(c)
	c.Check(s.persister.osRemove, gc.IsNil)
}

func (s *PersisterSuite) expectLockUnlock(sub *discovery.MockEtcdSubscriber, c *gc.C) {
	treeArg := mock.AnythingOfType("*etcd.Node")
	lockKey := PersisterLocksRoot + s.fragment.ContentName()

	// Expect callback on initial subscription.
	sub.On("OnEtcdUpdate", mock.MatchedBy(func(r *etcd.Response) bool {
		return r.Action == discovery.EtcdGetOp
	}), treeArg).Return().Once()

	// Expect a persister lock to be created.
	sub.On("OnEtcdUpdate", mock.MatchedBy(func(r *etcd.Response) bool {
		if r.Action != discovery.EtcdCreateOp {
			return false
		}
		c.Check(r.Node.Key, gc.Equals, lockKey)
		c.Check(r.Node.Value, gc.Equals, "route-key")
		return true
	}), treeArg).Return().Once()

	// Expect a persister lock to be released.
	sub.On("OnEtcdUpdate", mock.MatchedBy(func(r *etcd.Response) bool {
		if r.Action != discovery.EtcdDeleteOp {
			return false
		}
		c.Check(r.Node.Key, gc.Equals, lockKey)
		return true
	}), treeArg).Return().Once()
}

func (s *PersisterSuite) TestStringFunction(c *gc.C) {
	// Make sure that JSON marshaler doesn't choke on the |File| field.
	fp, err := os.Open("/dev/urandom")
	c.Assert(err, gc.IsNil)
	defer fp.Close()

	s.fragment.File = fp

	// Make sure the code handles multiple offsets of the same journal.
	frag2 := s.fragment
	frag2.Begin = 2000
	frag2.End = 3000

	s.persister.Persist(s.fragment)
	s.persister.Persist(frag2)

	c.Check(s.persister.String(), gc.Equals,
		`{"a/journal":["00000000000003e8-00000000000003f2-0102030405060708090a0b0c0d0e0f1011121314","00000000000007d0-0000000000000bb8-0102030405060708090a0b0c0d0e0f1011121314"]}`)
}

var _ = gc.Suite(&PersisterSuite{})
