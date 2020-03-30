package requestParser

import (
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/codex-db/batchInserter"
	"github.com/xmidt-org/voynicrypto"
)

type mockEncrypter struct {
	mock.Mock
}

func (md *mockEncrypter) EncryptMessage(message []byte) ([]byte, []byte, error) {
	args := md.Called(message)
	return message, []byte{}, args.Error(0)
}

func (*mockEncrypter) GetAlgorithm() voynicrypto.AlgorithmType {
	return voynicrypto.None
}

func (*mockEncrypter) GetKID() string {
	return "none"
}

type mockBlacklist struct {
	mock.Mock
}

func (mb *mockBlacklist) InList(ID string) (reason string, ok bool) {
	args := mb.Called(ID)
	reason = args.String(0)
	ok = args.Bool(1)
	return
}

type mockInserter struct {
	mock.Mock
}

func (i *mockInserter) Insert(record batchInserter.RecordWithTime) error {
	args := i.Called(record)
	return args.Error(0)
}

type mockTimeTracker struct {
	mock.Mock
}

func (m *mockTimeTracker) TrackTime(t time.Duration) {
	m.Called(t)
}
