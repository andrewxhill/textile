package threaddb

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/alecthomas/jsonschema"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/interface-go-ipfs-core/path"
	dbc "github.com/textileio/go-threads/api/client"
	"github.com/textileio/go-threads/core/thread"
	db "github.com/textileio/go-threads/db"
	powc "github.com/textileio/powergate/api/client"
	"github.com/textileio/powergate/ffs"
	"github.com/textileio/textile/buckets"
	mdb "github.com/textileio/textile/mongodb"
)

const Version = 1

var (
	bucketsSchema  *jsonschema.Schema
	bucketsIndexes = []db.Index{{
		Path: "path",
	}}
	bucketsConfig db.CollectionConfig

	// ffsDefaultCidConfig is a default hardcoded CidConfig to be used
	// on newly created FFS instances as the default CidConfig of archived Cids,
	// if none is provided in constructor.
	ffsDefaultCidConfig = ffs.DefaultConfig{
		Hot: ffs.HotConfig{
			Enabled:       false,
			AllowUnfreeze: true,
			Ipfs: ffs.IpfsConfig{
				AddTimeout: 60 * 2,
			},
		},
		Cold: ffs.ColdConfig{
			Enabled: true,
			Filecoin: ffs.FilConfig{
				RepFactor:       10,     // Aim high for testnet
				DealMinDuration: 200000, // ~2 months
			},
		},
	}
)

// Bucket represents the buckets threaddb collection schema.
type Bucket struct {
	Key       string          `json:"_id"`
	Owner     string          `json:"owner"`
	Name      string          `json:"name"`
	Version   int             `json:"version"`
	EncKey    string          `json:"key,omitempty"`
	Path      string          `json:"path"`
	Items     map[string]Item `json:"items"`
	Archives  Archives        `json:"archives"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
}

// Item describes details about a bucket item (a file or folder).
type Item struct {
	Cid       string `json:"cid"`
	ACL       ACL    `json:"acl"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ACL describes access rules for an item.
type ACL struct {
	Write  []string `json:"$w"`
	Read   []string `json:"$r"`
	Delete []string `json:"$d"`
}

// Archives contains all archives for a single bucket.
type Archives struct {
	Current Archive   `json:"current"`
	History []Archive `json:"history"`
}

// Archive is a single archive containing a list of deals.
type Archive struct {
	Cid   string `json:"cid"`
	Deals []Deal `json:"deals"`
}

// Deal contains details about a Filecoin deal.
type Deal struct {
	ProposalCid string `json:"proposal_cid"`
	Miner       string `json:"miner"`
}

// GetEncKey returns the encryption key as bytes if present.
func (b *Bucket) GetEncKey() []byte {
	if b.EncKey == "" {
		return nil
	}
	key, _ := base64.StdEncoding.DecodeString(b.EncKey)
	return key
}

// UpsertItemAtPath adds a new item or updates the existing item at path.
func (b *Bucket) UpsertItemAtPath(pth string, cid cid.Cid, updated time.Time) error {
	nanos := updated.UnixNano()
	x, ok := b.Items[pth]
	if ok && x.Cid != cid.String() {
		x.Cid = cid.String()
		x.UpdatedAt = nanos
		b.Items[pth] = x
	} else {
		var acl []string
		if b.Owner != "" {
			acl = []string{b.Owner}
		}
		b.Items[pth] = Item{
			Cid: cid.String(),
			ACL: ACL{
				Write:  acl,
				Read:   acl,
				Delete: acl,
			},
			CreatedAt: nanos,
			UpdatedAt: nanos,
		}
	}
	return nil
}

// BucketOptions defines options for interacting with buckets.
type BucketOptions struct {
	Name  string
	Key   []byte
	Token thread.Token
}

// BucketOption holds a bucket option.
type BucketOption func(*BucketOptions)

// WithNewBucketName specifies a name for a bucket.
// Note: This is only valid when creating a new bucket.
func WithNewBucketName(n string) BucketOption {
	return func(args *BucketOptions) {
		args.Name = n
	}
}

// WithNewBucketKey sets the bucket encryption key.
func WithNewBucketKey(k []byte) BucketOption {
	return func(args *BucketOptions) {
		args.Key = k
	}
}

// WithNewBucketToken sets the threaddb token.
func WithNewBucketToken(t thread.Token) BucketOption {
	return func(args *BucketOptions) {
		args.Token = t
	}
}

func init() {
	reflector := jsonschema.Reflector{ExpandedStruct: true}
	bucketsSchema = reflector.Reflect(&Bucket{})
	bucketsConfig = db.CollectionConfig{
		Name:    buckets.CollectionName,
		Schema:  bucketsSchema,
		Indexes: bucketsIndexes,
		ValidatorFunc: `
			var type = event.patch.type
			var patch = event.patch.json_patch
			switch (type) {
			  case "create":
				if (patch.owner !== author) {
				  return "author must match new bucket owner"
				}
				break
			  case "save":
				var keys = Object.keys(patch.items)
				for (i = 0; i < keys.length; i++) {
				  var p = patch.items[keys[i]]
				  if (p.acl && author !== instance.owner) {
					return "only owner can modify bucket access rules"
				  }
				  var x = instance.items[keys[i]]
				  if (x) {
					if (x.acl.$w.indexOf(author) === -1) {
					  return "author does not have write access"
					}
				  } else {
					if (author !== instance.owner) {
					  return "only owner can create new bucket items"
					}
				  }
				}
				break
			  case "delete":
				if (event.patch.owner !== author) {
				  return "author must match new bucket owner"
				}
				break
			}
			return true
		`,
	}
}

// Buckets is a wrapper around a threaddb collection that performs object storage on IPFS and Filecoin.
type Buckets struct {
	Collection

	ffsCol   *mdb.FFSInstances
	pgClient *powc.Client

	buckCidConfig ffs.DefaultConfig

	lock   sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewBuckets returns a new buckets collection mananger.
func NewBuckets(tc *dbc.Client, pgc *powc.Client, col *mdb.FFSInstances, defaultCidConfig *ffs.DefaultConfig) (*Buckets, error) {
	buckCidConfig := ffsDefaultCidConfig
	if defaultCidConfig != nil {
		buckCidConfig = *defaultCidConfig
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Buckets{
		Collection: Collection{
			c:      tc,
			config: bucketsConfig,
		},
		ffsCol:   col,
		pgClient: pgc,

		buckCidConfig: buckCidConfig,

		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Create a bucket instance.
func (b *Buckets) New(ctx context.Context, dbID thread.ID, key string, pth path.Path, owner thread.PubKey, opts ...BucketOption) (*Bucket, error) {
	args := &BucketOptions{}
	for _, opt := range opts {
		opt(args)
	}
	var encKey string
	if args.Key != nil {
		encKey = base64.StdEncoding.EncodeToString(args.Key)
	}
	if args.Token.Defined() {
		tokenOwner, err := args.Token.PubKey()
		if err != nil {
			return nil, err
		}
		if tokenOwner != nil && owner.String() != tokenOwner.String() {
			return nil, fmt.Errorf("creating bucket: token owner mismatch")
		}
	}
	now := time.Now().UnixNano()
	bucket := &Bucket{
		Key:       key,
		Name:      args.Name,
		Version:   Version,
		EncKey:    encKey,
		Path:      pth.String(),
		Items:     make(map[string]Item),
		Archives:  Archives{Current: Archive{Deals: []Deal{}}, History: []Archive{}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if owner != nil {
		bucket.Owner = owner.String()
	}
	id, err := b.Create(ctx, dbID, bucket, WithToken(args.Token))
	if err != nil {
		return nil, fmt.Errorf("creating bucket in thread: %s", err)
	}
	bucket.Key = string(id)

	if err := b.createFFSInstance(ctx, key); err != nil {
		return nil, fmt.Errorf("creating FFS instance for bucket: %s", err)
	}
	return bucket, nil
}

// IsArchivingEnabled returns whether or not Powergate archiving is enabled.
func (b *Buckets) IsArchivingEnabled() bool {
	return b.pgClient != nil
}

func (b *Buckets) createFFSInstance(ctx context.Context, bucketKey string) error {
	b.lock.Lock()
	defer b.lock.Unlock()
	// If the Powergate client isn't configured, don't do anything.
	if b.pgClient == nil {
		return nil
	}
	_, token, err := b.pgClient.FFS.Create(ctx)
	if err != nil {
		return fmt.Errorf("creating FFS instance: %s", err)
	}

	ctxFFS := context.WithValue(ctx, powc.AuthKey, token)
	i, err := b.pgClient.FFS.Info(ctxFFS)
	if err != nil {
		return fmt.Errorf("getting information about created ffs instance: %s", err)
	}
	waddr := i.Balances[0].Addr
	if err := b.ffsCol.Create(ctx, bucketKey, token, waddr); err != nil {
		return fmt.Errorf("saving FFS instances data: %s", err)
	}
	defaultBucketCidConfig := ffs.DefaultConfig{
		Cold:       b.buckCidConfig.Cold,
		Hot:        b.buckCidConfig.Hot,
		Repairable: b.buckCidConfig.Repairable,
	}
	defaultBucketCidConfig.Cold.Filecoin.Addr = waddr
	if err := b.pgClient.FFS.SetDefaultConfig(ctxFFS, defaultBucketCidConfig); err != nil {
		return fmt.Errorf("setting default bucket FFS cidconfig: %s", err)
	}
	return nil
}

// SaveSafe a bucket instance.
func (b *Buckets) SaveSafe(ctx context.Context, dbID thread.ID, bucket *Bucket, opts ...Option) error {
	ensureNoNulls(bucket)
	return b.Save(ctx, dbID, bucket, opts...)
}

func ensureNoNulls(b *Bucket) {
	if b.Items == nil {
		b.Items = make(map[string]Item)
	}
	if len(b.Archives.History) == 0 {
		current := b.Archives.Current
		if len(current.Deals) == 0 {
			b.Archives.Current = Archive{Deals: []Deal{}}
		}
		b.Archives = Archives{Current: current, History: []Archive{}}
	}
}

// ArchiveStatus returns the last known archive status on Powergate. If the return status is Failed,
// an extra string with the error message is provided.
func (b *Buckets) ArchiveStatus(ctx context.Context, key string) (ffs.JobStatus, string, error) {
	ffsi, err := b.ffsCol.Get(ctx, key)
	if err != nil {
		return ffs.Failed, "", fmt.Errorf("getting ffs instance data: %s", err)
	}

	if ffsi.Archives.Current.JobID == "" {
		return ffs.Failed, "", buckets.ErrNoCurrentArchive
	}
	current := ffsi.Archives.Current
	if current.Aborted {
		return ffs.Failed, "", fmt.Errorf("job status tracking was aborted: %s", current.AbortedMsg)
	}
	return ffs.JobStatus(current.JobStatus), current.FailureMsg, nil
}

// ArchiveWatch allows to have the last log execution for the last archive, plus realtime
// human-friendly log output of how the current archive is executing.
// If the last archive is already done, it will simply return the log history and close the channel.
func (b *Buckets) ArchiveWatch(ctx context.Context, key string, ch chan<- string) error {
	ffsi, err := b.ffsCol.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("getting ffs instance data: %s", err)
	}

	if ffsi.Archives.Current.JobID == "" {
		return buckets.ErrNoCurrentArchive
	}
	current := ffsi.Archives.Current
	if current.Aborted {
		return fmt.Errorf("job status tracking was aborted: %s", current.AbortedMsg)
	}
	c, err := cid.Cast(current.Cid)
	if err != nil {
		return fmt.Errorf("parsing current archive cid: %s", err)
	}
	ctx = context.WithValue(ctx, powc.AuthKey, ffsi.FFSToken)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ffsCh := make(chan powc.LogEvent)
	if err := b.pgClient.FFS.WatchLogs(ctx, ffsCh, c, powc.WithJidFilter(ffs.JobID(current.JobID)), powc.WithHistory(true)); err != nil {
		return fmt.Errorf("watching log events in Powergate: %s", err)
	}
	for le := range ffsCh {
		if le.Err != nil {
			return le.Err
		}
		ch <- le.LogEntry.Msg
	}
	return nil
}

func (b *Buckets) Close() error {
	b.cancel()
	b.wg.Wait()
	return nil
}
