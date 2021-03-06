package mongodb_test

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/textileio/go-threads/core/thread"
	. "github.com/textileio/textile/mongodb"
)

func TestAccounts_CreateDev(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	created, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)
	assert.Equal(t, Dev, created.Type)
	assert.Equal(t, "jon", created.Username)
	assert.Equal(t, "jon@doe.com", created.Email)
	assert.NotEmpty(t, created.Key)
	assert.NotEmpty(t, created.Secret)

	_, err = col.CreateDev(context.Background(), "jon", "jon2@doe.com")
	require.Error(t, err)
	_, err = col.CreateDev(context.Background(), "jon2", "jon@doe.com")
	require.Error(t, err)

	_, mem, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	_, err = col.CreateOrg(context.Background(), "jon", []Member{{
		Key:      mem,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.Error(t, err)
}

func TestAccounts_Get(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	created, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)

	got, err := col.Get(context.Background(), created.Key)
	require.NoError(t, err)
	assert.Equal(t, created.Key, got.Key)
}

func TestAccounts_BucketsTotalSize(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	created, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)

	err = col.SetBucketsTotalSize(context.Background(), created.Key, 1234)
	require.NoError(t, err)

	got, err := col.Get(context.Background(), created.Key)
	require.NoError(t, err)
	assert.Equal(t, int64(1234), got.BucketsTotalSize)
}

func TestAccounts_GetByUsernameOrEmail(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	created, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)

	got, err := col.GetByUsernameOrEmail(context.Background(), "jon")
	require.NoError(t, err)
	assert.Equal(t, created.Key, got.Key)

	got, err = col.GetByUsernameOrEmail(context.Background(), "jon@doe.com")
	require.NoError(t, err)
	assert.Equal(t, created.Key, got.Key)

	_, err = col.GetByUsernameOrEmail(context.Background(), "jon2")
	require.Error(t, err)
}

func TestAccounts_ValidateUsername(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	tests := map[string]bool{
		"":      false,
		" ":     false,
		"f oo":  false,
		"-":     false,
		"-foo":  false,
		"foo-":  false,
		"f-o-o": false,
		"fo--o": false,

		"foo":  true,
		"fo-o": true,
		"fOO":  true,
		"f00":  true,
	}

	for un, valid := range tests {
		err := col.ValidateUsername(un)
		assert.Equal(t, valid, err == nil)
	}
}

func TestAccounts_IsUsernameAvailable(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	err = col.IsUsernameAvailable(context.Background(), "jon")
	require.NoError(t, err)

	_, err = col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)

	err = col.IsUsernameAvailable(context.Background(), "jon")
	require.Error(t, err)
}

func TestAccounts_SetToken(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	created, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)

	iss, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	tok, err := thread.NewToken(iss, thread.NewLibp2pPubKey(created.Key))
	require.NoError(t, err)
	err = col.SetToken(context.Background(), created.Key, tok)
	require.NoError(t, err)

	got, err := col.Get(context.Background(), created.Key)
	require.NoError(t, err)
	assert.NotEmpty(t, got.Token)
}

func TestAccounts_ListMembers(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	one, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)
	two, err := col.CreateDev(context.Background(), "jane", "jane@doe.com")
	require.NoError(t, err)
	_, err = col.CreateDev(context.Background(), "jone", "jone@doe.com")
	require.NoError(t, err)

	list, err := col.ListMembers(context.Background(), []Member{{Key: one.Key}, {Key: two.Key}})
	require.NoError(t, err)
	assert.Equal(t, 2, len(list))
}

func TestAccounts_Delete(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	created, err := col.CreateDev(context.Background(), "jon", "jon@doe.com")
	require.NoError(t, err)

	err = col.Delete(context.Background(), created.Key)
	require.NoError(t, err)
	_, err = col.Get(context.Background(), created.Key)
	require.Error(t, err)
}

func TestAccounts_CreateOrg(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)
	assert.Equal(t, Org, created.Type)
	assert.Equal(t, created.Name, "test")
	assert.NotNil(t, created.Key)
	assert.True(t, created.CreatedAt.Unix() > 0)

	_, err = col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.Error(t, err)

	_, err = col.CreateOrg(context.Background(), "empty", []Member{})
	require.Error(t, err)

	_, err = col.CreateDev(context.Background(), "test", "jon@doe.com")
	require.Error(t, err)
}

func TestAccounts_GetByUsername(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	got, err := col.GetByUsername(context.Background(), created.Username)
	require.NoError(t, err)
	assert.Equal(t, created.Key, got.Key)
}

func TestAccounts_IsNameAvailable(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, err = col.IsNameAvailable(context.Background(), "test")
	require.NoError(t, err)

	_, mem, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "Test!", []Member{{
		Key:      mem,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)
	assert.Equal(t, created.Username, "Test")

	name, err := col.IsNameAvailable(context.Background(), "Test!")
	require.Error(t, err)
	assert.Equal(t, created.Username, name)
}

func TestAccounts_ListByMember(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	list, err := col.ListByMember(context.Background(), mem)
	require.NoError(t, err)
	assert.Equal(t, 1, len(list))
	assert.Equal(t, created.Name, list[0].Name)
}

func TestAccounts_ListByOwner(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem1, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem1,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	list, err := col.ListByOwner(context.Background(), mem1)
	require.NoError(t, err)
	assert.Equal(t, 1, len(list))
	assert.Equal(t, created.Name, list[0].Name)

	_, mem2, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	err = col.AddMember(context.Background(), created.Username, Member{
		Key:      mem2,
		Username: "member",
		Role:     OrgMember,
	})
	require.NoError(t, err)
	list, err = col.ListByOwner(context.Background(), mem2)
	require.NoError(t, err)
	assert.Equal(t, 0, len(list))
}

func TestAccounts_IsOwner(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem1, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem1,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	_, mem2, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	err = col.AddMember(context.Background(), created.Username, Member{
		Key:      mem2,
		Username: "member",
		Role:     OrgMember,
	})
	require.NoError(t, err)

	is, err := col.IsOwner(context.Background(), created.Username, mem1)
	require.NoError(t, err)
	assert.True(t, is)
	is, err = col.IsOwner(context.Background(), created.Username, mem2)
	require.NoError(t, err)
	assert.False(t, is)
}

func TestAccounts_IsMember(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem1, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem1,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	_, mem2, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	err = col.AddMember(context.Background(), created.Username, Member{
		Key:      mem2,
		Username: "member",
		Role:     OrgMember,
	})
	require.NoError(t, err)

	is, err := col.IsMember(context.Background(), created.Username, mem2)
	require.NoError(t, err)
	assert.True(t, is)
	err = col.RemoveMember(context.Background(), created.Username, mem2)
	require.NoError(t, err)
	is, err = col.IsMember(context.Background(), created.Username, mem2)
	require.NoError(t, err)
	assert.False(t, is)
}

func TestAccounts_AddMember(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem1, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem1,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	_, mem2, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	err = col.AddMember(context.Background(), created.Username, Member{
		Key:      mem2,
		Username: "member",
		Role:     OrgMember,
	})
	require.NoError(t, err)
	err = col.AddMember(context.Background(), created.Username, Member{ // Add again should not duplicate entry
		Key:      mem2,
		Username: "member",
		Role:     OrgMember,
	})
	require.NoError(t, err)

	got, err := col.GetByUsername(context.Background(), created.Username)
	require.NoError(t, err)
	assert.Equal(t, 2, len(got.Members))
}

func TestAccounts_RemoveMember(t *testing.T) {
	db := newDB(t)
	col, err := NewAccounts(context.Background(), db)
	require.NoError(t, err)

	_, mem1, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	created, err := col.CreateOrg(context.Background(), "test", []Member{{
		Key:      mem1,
		Username: "test",
		Role:     OrgOwner,
	}})
	require.NoError(t, err)

	err = col.RemoveMember(context.Background(), created.Username, mem1)
	require.Error(t, err) // Can't remove the sole owner

	_, mem2, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	err = col.AddMember(context.Background(), created.Username, Member{
		Key:      mem2,
		Username: "member",
		Role:     OrgMember,
	})
	require.NoError(t, err)

	err = col.RemoveMember(context.Background(), created.Username, mem2)
	require.NoError(t, err)
	list, err := col.ListByMember(context.Background(), mem2)
	require.NoError(t, err)
	assert.Equal(t, 0, len(list))
}
