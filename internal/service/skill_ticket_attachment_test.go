package service

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// errNoRowsForTest 复用 sql.ErrNoRows,模拟 store 按 id 未命中时返回的标准“无行”错误,
// 供 service 通过 errors.Is(err, sql.ErrNoRows) 映射为 NotFound。
var errNoRowsForTest = sql.ErrNoRows

// fakeAttachmentStore 记录附件落库,供 service 单测。
type fakeAttachmentStore struct {
	rows map[string]sqlc.SkillTicketAttachment
}

func newFakeAttachmentStore() *fakeAttachmentStore {
	return &fakeAttachmentStore{rows: map[string]sqlc.SkillTicketAttachment{}}
}
func (f *fakeAttachmentStore) CreateSkillTicketAttachment(_ context.Context, a sqlc.CreateSkillTicketAttachmentParams) error {
	f.rows[a.ID] = sqlc.SkillTicketAttachment{
		ID: a.ID, TicketID: a.TicketID, ObjectPath: a.ObjectPath, FileName: a.FileName, FileSize: a.FileSize,
	}
	return nil
}
func (f *fakeAttachmentStore) ListSkillTicketAttachments(_ context.Context, ticketID string) ([]sqlc.SkillTicketAttachment, error) {
	var out []sqlc.SkillTicketAttachment
	for _, r := range f.rows {
		if r.TicketID == ticketID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeAttachmentStore) GetSkillTicketAttachment(_ context.Context, id string) (sqlc.SkillTicketAttachment, error) {
	r, ok := f.rows[id]
	if !ok {
		return sqlc.SkillTicketAttachment{}, errNoRowsForTest
	}
	return r, nil
}

// fakeBlobs 复用 Plan1/平台库的 LibraryBlobStore,内存实现。
type fakeBlobs struct{ data map[string][]byte }

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{data: map[string][]byte{}} }
func (f *fakeBlobs) PutLibrarySkill(source, ref, version, ext string, data []byte) (string, error) {
	key := "library/" + source + "/" + ref + "/" + version + "." + ext
	f.data[key] = data
	return key, nil
}
func (f *fakeBlobs) DeleteLibrarySkill(rel string) error { delete(f.data, rel); return nil }
func (f *fakeBlobs) OpenLibrarySkill(rel string) (io.ReadCloser, error) {
	b, ok := f.data[rel]
	if !ok {
		return nil, errNoRowsForTest
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func memberAP() auth.Principal { return auth.Principal{UserID: "u-mem", OrgID: "org-1", Role: domain.UserRoleOrgMember} }

// TestSkillTicketAttachmentService_Add_OK 覆盖正常上传路径:写对象存储 + 落库,
// 校验返回文件名与对象 key 落在 library/ticket-attachment/<ticketID>/ 前缀下。
func TestSkillTicketAttachmentService_Add_OK(t *testing.T) {
	store := newFakeAttachmentStore()
	blobs := newFakeBlobs()
	svc := NewSkillTicketAttachmentService(store, blobs)
	res, err := svc.Add(context.Background(), memberAP(), "ticket-1", "周报模板.docx", []byte("doc-bytes"))
	require.NoError(t, err)
	assert.Equal(t, "周报模板.docx", res.FileName)
	assert.True(t, strings.HasPrefix(store.rows[res.ID].ObjectPath, "library/ticket-attachment/ticket-1/"))
}

// TestSkillTicketAttachmentService_Add_Invalid 覆盖入参非法边界:空白文件名应返回 Invalid 哨兵错误。
func TestSkillTicketAttachmentService_Add_Invalid(t *testing.T) {
	svc := NewSkillTicketAttachmentService(newFakeAttachmentStore(), newFakeBlobs())
	_, err := svc.Add(context.Background(), memberAP(), "ticket-1", "  ", []byte("x"))
	require.ErrorIs(t, err, ErrSkillTicketAttachmentInvalid)
}

// TestSkillTicketAttachmentService_Open_OK 覆盖下载路径:按正确的 ticketID + attachmentID 取回原始字节与文件名(供 Content-Disposition)。
func TestSkillTicketAttachmentService_Open_OK(t *testing.T) {
	store := newFakeAttachmentStore()
	blobs := newFakeBlobs()
	svc := NewSkillTicketAttachmentService(store, blobs)
	res, _ := svc.Add(context.Background(), memberAP(), "ticket-1", "a.txt", []byte("hello"))
	// 传入附件所属的 ticketID,应成功取回内容
	rc, name, err := svc.Open(context.Background(), "ticket-1", res.ID)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "hello", string(got))
	assert.Equal(t, "a.txt", name)
}

// TestSkillTicketAttachmentService_Open_WrongTicket 覆盖 IDOR 防御:
// 用不属于该附件的工单 ID 调用 Open,应返回 ErrSkillTicketAttachmentNotFound,
// 不泄露该附件在其他工单中的存在性。
func TestSkillTicketAttachmentService_Open_WrongTicket(t *testing.T) {
	store := newFakeAttachmentStore()
	blobs := newFakeBlobs()
	svc := NewSkillTicketAttachmentService(store, blobs)
	// 附件属于 ticket-1,但用 ticket-2 的 ID 来查询
	res, _ := svc.Add(context.Background(), memberAP(), "ticket-1", "secret.txt", []byte("secret"))
	_, _, err := svc.Open(context.Background(), "ticket-2", res.ID)
	// 归属不匹配,应当不存在(防 IDOR)
	require.ErrorIs(t, err, ErrSkillTicketAttachmentNotFound)
}
