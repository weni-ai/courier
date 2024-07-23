package rapidpro

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
)

// getChannel will look up the channel with the passed in UUID and channel type.
// It will return an error if the channel does not exist or is not active.
func getChannel(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, channelUUID courier.ChannelUUID) (*DBChannel, error) {
	// look for the channel locally
	cachedChannel, localErr := getCachedChannel(channelType, channelUUID)

	// found it? return it
	if localErr == nil {
		return cachedChannel, nil
	}

	// check if channel any thread alread has a call loadChannelFromDB
	loadThrottled, _ := getChannelLoadLock(channelUUID)
	if loadThrottled != nil {
		//already loading channel by address from db? so every 200ms try to get from cache until its get cached from first load or after lock expire
		ticker := time.NewTicker(time.Millisecond * 200)
		for {
			<-ticker.C
			cachedChannel, err := getCachedChannel(channelType, channelUUID)
			if cachedChannel != nil {
				if err != nil { // found and is expired? clear from cache
					clearLocalChannel(channelUUID)
				}
				// already loaded and cached, return it
				return cachedChannel, nil
			}
		}
	}
	// not loaded from db yet, so throttle next loads until first one completes
	lockLoadChannel(channelUUID)

	// look in our database instead
	channel, dbErr := loadChannelFromDB(ctx, db, channelType, channelUUID)

	// loaded, so unlock the next loads to allow expired cachs to retrieve again
	unlockLoadChannel(channelUUID)

	// if it wasn't found in the DB, clear our cache and return that it wasn't found
	if dbErr == courier.ErrChannelNotFound {
		clearLocalChannel(channelUUID)
		return cachedChannel, fmt.Errorf("unable to find channel with type: %s and uuid: %s", channelType.String(), channelUUID.String())
	}

	// if we had some other db error, return it if our cached channel was only just expired
	if dbErr != nil && localErr == courier.ErrChannelExpired {
		return cachedChannel, nil
	}

	// no cached channel, oh well, we fail
	if dbErr != nil {
		return nil, dbErr
	}

	// we found it in the db, cache it locally
	cacheChannel(channel)
	return channel, nil
}

const lookupChannelFromUUIDSQL = `
SELECT 
	org_id, 
	ch.id as id, 
	ch.uuid as uuid, 
	ch.name as name, 
	channel_type, schemes, 
	address, role,
	ch.country as country, 
	ch.config as config, 
	org.config as org_config, 
	org.is_anon as org_is_anon
FROM 
	channels_channel ch
	JOIN orgs_org org on ch.org_id = org.id
WHERE 
	ch.uuid = $1 AND 
	ch.is_active = true AND 
	ch.org_id IS NOT NULL`

// ChannelForUUID attempts to look up the channel with the passed in UUID, returning it
func loadChannelFromDB(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, uuid courier.ChannelUUID) (*DBChannel, error) {
	channel := &DBChannel{UUID_: uuid}

	// select just the fields we need
	err := db.GetContext(ctx, channel, lookupChannelFromUUIDSQL, uuid)

	// we didn't find a match
	if err == sql.ErrNoRows {
		return nil, courier.ErrChannelNotFound
	}

	// other error
	if err != nil {
		return nil, err
	}

	// is it the right type?
	if channelType != courier.AnyChannelType && channelType != channel.ChannelType() {
		return nil, courier.ErrChannelWrongType
	}

	// found it, return it
	return channel, nil
}

// getCachedChannel returns a Channel object for the passed in type and UUID.
func getCachedChannel(channelType courier.ChannelType, uuid courier.ChannelUUID) (*DBChannel, error) {
	// first see if the channel exists in our local cache
	cacheMutex.RLock()
	channel, found := channelCache[uuid]
	cacheMutex.RUnlock()

	if found {
		// if it was found but the type is wrong, that's an error
		if channelType != courier.AnyChannelType && channel.ChannelType() != channelType {
			return nil, courier.ErrChannelWrongType
		}

		// if we've expired, we return it with an error
		if channel.expiration.Before(time.Now()) {
			return channel, courier.ErrChannelExpired
		}

		return channel, nil
	}

	return nil, courier.ErrChannelNotFound
}

func cacheChannel(channel *DBChannel) {
	channel.expiration = time.Now().Add(localTTL)

	cacheMutex.Lock()
	channelCache[channel.UUID()] = channel
	cacheMutex.Unlock()
}

func clearLocalChannel(uuid courier.ChannelUUID) {
	cacheMutex.Lock()
	delete(channelCache, uuid)
	cacheMutex.Unlock()
}

// channels stay cached in memory for a minute at a time
const localTTL = 60 * time.Second

var cacheMutex sync.RWMutex
var channelCache = make(map[courier.ChannelUUID]*DBChannel)

type LoadLock struct {
	expiration time.Time
}

var channelLoadLock = make(map[courier.ChannelUUID]*LoadLock)
var channelLoadLockMutex sync.Mutex

func getChannelLoadLock(channelUUID courier.ChannelUUID) (*LoadLock, error) {
	channelLoadLockMutex.Lock()
	defer channelLoadLockMutex.Unlock()
	locked, found := channelLoadLock[channelUUID]
	if found {
		if locked.expiration.Before(time.Now()) {
			return nil, errors.New("load lock expired")
		}
		return locked, nil
	}
	return nil, errors.New("not locked")
}

func lockLoadChannel(channelUUID courier.ChannelUUID) {
	channelLoadLockMutex.Lock()
	defer channelLoadLockMutex.Unlock()
	channelLoadLock[channelUUID] = &LoadLock{expiration: time.Now().Add(time.Second * 10)}
}

func unlockLoadChannel(channelUUID courier.ChannelUUID) {
	channelLoadLockMutex.Lock()
	defer channelLoadLockMutex.Unlock()
	delete(channelLoadLock, channelUUID)
}

// getChannelByAddress will look up the channel with the passed in address and channel type.
// It will return an error if the channel does not exist or is not active.
func getChannelByAddress(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, address courier.ChannelAddress) (*DBChannel, error) {
	// look for the channel locally
	cachedChannel, localErr := getCachedChannelByAddress(channelType, address)

	// found it? return it
	if localErr == nil {
		return cachedChannel, nil
	}

	// check if channel any thread alread has a call loadChannelByAddressFromDB
	lockLoad, _ := getChannelLoadByAddressLock(address)
	if lockLoad != nil {
		//already loading channel by address from db? so every 200ms try to get from cache until it get cached from first load or after lock expire
		ticker := time.NewTicker(time.Millisecond * 200)
		for {
			<-ticker.C
			cachedChannel, err := getCachedChannelByAddress(channelType, address)
			if cachedChannel != nil {
				if err != nil { // found and is expired? clear from cache
					clearLocalChannelByAddress(address)
				}
				// already loaded and cached, return it
				return cachedChannel, err
			}
		}
	}
	// not loaded from db yet, so lock next loads until first one completes
	lockLoadChannelByAddress(address)

	// look in our database instead
	channel, dbErr := loadChannelByAddressFromDB(ctx, db, channelType, address)

	// loaded, so unlock the next loads to allow expired cachs to retrieve again
	unlockLoadChannelByAddress(address)

	// if it wasn't found in the DB, clear our cache and return that it wasn't found
	if dbErr == courier.ErrChannelNotFound {
		clearLocalChannelByAddress(address)
		return cachedChannel, fmt.Errorf("unable to find channel with type: %s and address: %s", channelType.String(), address.String())
	}

	// if we had some other db error, return it if our cached channel was only just expired
	if dbErr != nil && localErr == courier.ErrChannelExpired {
		return cachedChannel, nil
	}

	// no cached channel, oh well, we fail
	if dbErr != nil {
		return nil, dbErr
	}

	// we found it in the db, cache it locally
	cacheChannelByAddress(channel)
	return channel, nil
}

const lookupChannelFromAddressSQL = `
SELECT
       org_id,
       ch.id as id,
       ch.uuid as uuid,
       ch.name as name,
       channel_type, schemes,
       address,
       ch.country as country,
       ch.config as config,
       org.config as org_config,
       org.is_anon as org_is_anon
FROM
       channels_channel ch
       JOIN orgs_org org on ch.org_id = org.id
WHERE
       ch.address = $1 AND
       ch.is_active = true AND
       ch.org_id IS NOT NULL`

// loadChannelByAddressFromDB get the channel with the passed in channel type and address from the DB, returning it
func loadChannelByAddressFromDB(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, address courier.ChannelAddress) (*DBChannel, error) {
	channel := &DBChannel{Address_: sql.NullString{String: address.String(), Valid: address == courier.NilChannelAddress}}

	// select just the fields we need
	err := db.GetContext(ctx, channel, lookupChannelFromAddressSQL, address)

	// we didn't find a match
	if err == sql.ErrNoRows {
		return nil, courier.ErrChannelNotFound
	}

	// other error
	if err != nil {
		return nil, err
	}

	// is it the right type?
	if channelType != courier.AnyChannelType && channelType != channel.ChannelType() {
		return nil, courier.ErrChannelWrongType
	}

	// found it, return it
	return channel, nil
}

// getCachedChannelByAddress returns a Channel object for the passed in type and address.
func getCachedChannelByAddress(channelType courier.ChannelType, address courier.ChannelAddress) (*DBChannel, error) {
	// first see if the channel exists in our local cache
	cacheByAddressMutex.RLock()
	channel, found := channelByAddressCache[address]
	cacheByAddressMutex.RUnlock()

	// do not consider the cache for empty addresses
	if found && address != courier.NilChannelAddress {
		// if it was found but the type is wrong, that's an error
		if channelType != courier.AnyChannelType && channel.ChannelType() != channelType {
			return nil, courier.ErrChannelWrongType
		}

		// if we've expired, we return it with an error
		if channel.expiration.Before(time.Now()) {
			return channel, courier.ErrChannelExpired
		}

		return channel, nil
	}

	return nil, courier.ErrChannelNotFound
}

func cacheChannelByAddress(channel *DBChannel) {
	channel.expiration = time.Now().Add(localTTL)

	// never cache if the address is nil or empty
	if channel.ChannelAddress() == courier.NilChannelAddress {
		return
	}

	cacheByAddressMutex.Lock()
	channelByAddressCache[channel.ChannelAddress()] = channel
	cacheByAddressMutex.Unlock()
}

func clearLocalChannelByAddress(address courier.ChannelAddress) {
	cacheByAddressMutex.Lock()
	delete(channelByAddressCache, address)
	cacheByAddressMutex.Unlock()
}

var cacheByAddressMutex sync.RWMutex
var channelByAddressCache = make(map[courier.ChannelAddress]*DBChannel)

// logic for throttle the request to channels by address to avoid multiple queries to db
var channelLoadByAddressLocks = make(map[courier.ChannelAddress]*LoadLock)
var channelLoadByAddressLockMutex sync.Mutex

func getChannelLoadByAddressLock(address courier.ChannelAddress) (*LoadLock, error) {
	channelLoadByAddressLockMutex.Lock()
	locked, found := channelLoadByAddressLocks[address]
	channelLoadByAddressLockMutex.Unlock()
	if found {
		if locked.expiration.Before(time.Now()) {
			return nil, errors.New("load lock expired")
		}
		return locked, nil
	}
	return nil, errors.New("not locked")
}

func lockLoadChannelByAddress(address courier.ChannelAddress) {
	if address == courier.NilChannelAddress {
		return
	}
	channelLoadByAddressLockMutex.Lock()
	channelLoadByAddressLocks[address] = &LoadLock{expiration: time.Now().Add(time.Second * 10)}
	channelLoadByAddressLockMutex.Unlock()
}

func unlockLoadChannelByAddress(address courier.ChannelAddress) {
	channelLoadByAddressLockMutex.Lock()
	delete(channelLoadByAddressLocks, address)
	channelLoadByAddressLockMutex.Unlock()
}

//-----------------------------------------------------------------------------
// Channel Implementation
//-----------------------------------------------------------------------------

// DBChannel is the RapidPro specific concrete type satisfying the courier.Channel interface
type DBChannel struct {
	OrgID_       OrgID               `db:"org_id"`
	ID_          courier.ChannelID   `db:"id"`
	ChannelType_ courier.ChannelType `db:"channel_type"`
	Schemes_     pq.StringArray      `db:"schemes"`
	UUID_        courier.ChannelUUID `db:"uuid"`
	Name_        sql.NullString      `db:"name"`
	Address_     sql.NullString      `db:"address"`
	Country_     sql.NullString      `db:"country"`
	Config_      utils.NullMap       `db:"config"`
	Role_        string              `db:"role"`

	OrgConfig_ utils.NullMap `db:"org_config"`
	OrgIsAnon_ bool          `db:"org_is_anon"`

	expiration time.Time
}

// OrgID returns the id of the org this channel is for
func (c *DBChannel) OrgID() OrgID { return c.OrgID_ }

// OrgIsAnon returns the org for this channel is anonymous
func (c *DBChannel) OrgIsAnon() bool { return c.OrgIsAnon_ }

// ChannelType returns the type of this channel
func (c *DBChannel) ChannelType() courier.ChannelType { return c.ChannelType_ }

// Name returns the name of this channel
func (c *DBChannel) Name() string { return c.Name_.String }

// Schemes returns the schemes this channels supports
func (c *DBChannel) Schemes() []string { return []string(c.Schemes_) }

// ID returns the id of this channel
func (c *DBChannel) ID() courier.ChannelID { return c.ID_ }

// UUID returns the UUID of this channel
func (c *DBChannel) UUID() courier.ChannelUUID { return c.UUID_ }

// Address returns the address of this channel as a string
func (c *DBChannel) Address() string { return c.Address_.String }

// ChannelAddress returns the address of this channel
func (c *DBChannel) ChannelAddress() courier.ChannelAddress {
	if !c.Address_.Valid {
		return courier.NilChannelAddress
	}

	return courier.ChannelAddress(c.Address_.String)
}

// Country returns the country code for this channel if any
func (c *DBChannel) Country() string { return c.Country_.String }

// IsScheme returns whether this channel serves only the passed in scheme
func (c *DBChannel) IsScheme(scheme string) bool {
	return len(c.Schemes_) == 1 && c.Schemes_[0] == scheme
}

// Roles returns the roles of this channel
func (c *DBChannel) Roles() []courier.ChannelRole {
	roles := []courier.ChannelRole{}
	for _, char := range strings.Split(c.Role_, "") {
		roles = append(roles, courier.ChannelRole(char))
	}
	return roles
}

// HasRole returns whether the passed in channel supports the passed role
func (c *DBChannel) HasRole(role courier.ChannelRole) bool {
	for _, r := range c.Roles() {
		if r == role {
			return true
		}
	}
	return false
}

// ConfigForKey returns the config value for the passed in key, or defaultValue if it isn't found
func (c *DBChannel) ConfigForKey(key string, defaultValue interface{}) interface{} {
	// no value, return our default value
	if !c.Config_.Valid {
		return defaultValue
	}

	value, found := c.Config_.Map[key]
	if !found {
		return defaultValue
	}
	return value
}

// OrgConfigForKey returns the org config value for the passed in key, or defaultValue if it isn't found
func (c *DBChannel) OrgConfigForKey(key string, defaultValue interface{}) interface{} {
	// no value, return our default value
	if !c.OrgConfig_.Valid {
		return defaultValue
	}

	value, found := c.OrgConfig_.Map[key]
	if !found {
		return defaultValue
	}
	return value
}

// CallbackDomain returns the callback domain to use for this channel
func (c *DBChannel) CallbackDomain(fallbackDomain string) string {
	value, found := c.Config_.Map[courier.ConfigCallbackDomain]
	strValue, isStr := value.(string)
	if !found || !isStr {
		return fallbackDomain
	}
	return strValue
}

// StringConfigForKey returns the config value for the passed in key, or defaultValue if it isn't found
func (c *DBChannel) StringConfigForKey(key string, defaultValue string) string {
	val := c.ConfigForKey(key, defaultValue)
	str, isStr := val.(string)
	if !isStr {
		return defaultValue
	}
	return str
}

// BoolConfigForKey returns the config value for the passed in key, or defaultValue if it isn't found
func (c *DBChannel) BoolConfigForKey(key string, defaultValue bool) bool {
	val := c.ConfigForKey(key, defaultValue)
	b, isBool := val.(bool)
	if !isBool {
		return defaultValue
	}
	return b
}

// IntConfigForKey returns the config value for the passed in key
func (c *DBChannel) IntConfigForKey(key string, defaultValue int) int {
	val := c.ConfigForKey(key, defaultValue)

	// golang unmarshals number literals in JSON into float64s by default
	f, isFloat := val.(float64)
	if isFloat {
		return int(f)
	}

	str, isStr := val.(string)
	if isStr {
		i, err := strconv.Atoi(str)
		if err == nil {
			return i
		}
	}
	return defaultValue
}

// supportsScheme returns whether the passed in channel supports the passed in scheme
func (c *DBChannel) supportsScheme(scheme string) bool {
	for _, s := range c.Schemes_ {
		if s == scheme {
			return true
		}
	}
	return false
}
