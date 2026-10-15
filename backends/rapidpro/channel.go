package rapidpro

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
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

	// look in our database instead, sharing a single load with concurrent lookups of the same channel
	channel, dbErr := loadChannelOnce(ctx, channelLoadKey(channelType, channelUUID.String()), func(loadCtx context.Context) (*DBChannel, error) {
		channel, err := loadChannelFromDB(loadCtx, db, channelType, channelUUID)
		if err == nil {
			cacheChannel(channel)
		}
		return channel, err
	})

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

	return channel, nil
}

const channelLoadTimeout = backendTimeout

var channelLoads singleflight.Group

func channelLoadKey(channelType courier.ChannelType, lookupValue string) string {
	return channelType.String() + ":" + lookupValue
}

// loadChannelOnce runs load at most once at a time per key: concurrent callers share its result, including errors.
// The load is detached from the first caller's cancellation, and each caller stops waiting when its own ctx is done.
func loadChannelOnce(ctx context.Context, key string, load func(context.Context) (*DBChannel, error)) (*DBChannel, error) {
	results := channelLoads.DoChan(key, func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), channelLoadTimeout)
		defer cancel()
		return load(loadCtx)
	})

	select {
	case result := <-results:
		if result.Err != nil {
			return nil, result.Err
		}
		channel, _ := result.Val.(*DBChannel)
		return channel, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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

// channels stay cached in memory for 5 minutes
const localTTL = 5 * time.Minute

var cacheMutex sync.RWMutex
var channelCache = make(map[courier.ChannelUUID]*DBChannel)

// getChannelByAddress will look up the channel with the passed in address and channel type.
// It will return an error if the channel does not exist or is not active.
func getChannelByAddress(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, address courier.ChannelAddress) (*DBChannel, error) {
	// look for the channel locally
	cachedChannel, localErr := getCachedChannelByAddress(channelType, address)

	// found it? return it
	if localErr == nil {
		return cachedChannel, nil
	}

	// look in our database instead, sharing a single load with concurrent lookups of the same address
	channel, dbErr := loadChannelOnce(ctx, channelLoadKey(channelType, address.String()), func(loadCtx context.Context) (*DBChannel, error) {
		channel, err := loadChannelByAddressFromDB(loadCtx, db, channelType, address)
		if err == nil {
			cacheChannelByAddress(channel)
		}
		return channel, err
	})

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

	return channel, nil
}

// getChannelByAddressWithRouterToken returns the channel with the passed in type and address and a value from config by key
func getChannelByAddressWithRouterToken(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, address courier.ChannelAddress, routerToken string) (*DBChannel, error) {
	// look for the channel locally
	cachedChannel, localErr := getCachedChannelByAddressWithRouterToken(channelType, routerToken)

	// found it? return it
	if localErr == nil {
		return cachedChannel, nil
	}

	// look in our database instead, sharing a single load with concurrent lookups of the same address and router token
	channel, dbErr := loadChannelOnce(ctx, channelLoadKey(channelType, address.String()+":"+routerToken), func(loadCtx context.Context) (*DBChannel, error) {
		channel, err := loadChannelByAddressWithRouterTokenFromDB(loadCtx, db, channelType, address, routerToken)
		if err == nil {
			cacheChannelByAddressWithRouterToken(channel, routerToken)
		}
		return channel, err
	})

	// if it wasn't found in the DB, clear our cache and return that it wasn't found
	if dbErr == courier.ErrChannelNotFound {
		clearLocalChannelByAddressWithRouterToken(routerToken)
		return cachedChannel, fmt.Errorf("unable to find channel with type: %s and address: %s and router token: %s", channelType.String(), address.String(), routerToken)
	}

	// if we had some other db error, return it if our cached channel was only just expired
	if dbErr != nil && localErr == courier.ErrChannelExpired {
		return cachedChannel, nil
	}

	// no cached channel, oh well, we fail
	if dbErr != nil {
		return nil, dbErr
	}

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

const lookupChannelFromAddressWithRouterTokenSQL = `
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
							(ch.config::jsonb)->>'router_token' = $2 AND
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

var cacheByAddressWithRouterTokenMutex sync.RWMutex
var channelByAddressWithRouterTokenCache = make(map[string]*DBChannel)

func getCachedChannelByAddressWithRouterToken(channelType courier.ChannelType, routerToken string) (*DBChannel, error) {
	cacheByAddressWithRouterTokenMutex.RLock()
	channel, found := channelByAddressWithRouterTokenCache[routerToken]
	cacheByAddressWithRouterTokenMutex.RUnlock()

	if found && routerToken != "" {
		if channelType != courier.AnyChannelType && channel.ChannelType() != channelType {
			return nil, courier.ErrChannelWrongType
		}

		if channel.expiration.Before(time.Now()) {
			return channel, courier.ErrChannelExpired
		}

		return channel, nil
	}

	return nil, courier.ErrChannelNotFound
}

func cacheChannelByAddressWithRouterToken(channel *DBChannel, routerToken string) {
	channel.expiration = time.Now().Add(localTTL)

	cacheByAddressWithRouterTokenMutex.Lock()
	channelByAddressWithRouterTokenCache[routerToken] = channel
	cacheByAddressWithRouterTokenMutex.Unlock()
}

func clearLocalChannelByAddressWithRouterToken(routerToken string) {
	cacheByAddressWithRouterTokenMutex.Lock()
	delete(channelByAddressWithRouterTokenCache, routerToken)
	cacheByAddressWithRouterTokenMutex.Unlock()
}

func loadChannelByAddressWithRouterTokenFromDB(ctx context.Context, db *sqlx.DB, channelType courier.ChannelType, address courier.ChannelAddress, routerToken string) (*DBChannel, error) {
	channel := &DBChannel{Address_: sql.NullString{String: address.String(), Valid: address == courier.NilChannelAddress}}

	// select just the fields we need
	err := db.GetContext(ctx, channel, lookupChannelFromAddressWithRouterTokenSQL, address, routerToken)

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

const updateChannelConfigSQL = `
UPDATE channels_channel 
SET config = $1
WHERE uuid = $2 AND is_active = true`

const updateChannelConfigByWabaIDSQL = `
UPDATE channels_channel 
SET config = (config::jsonb || $1::jsonb)::text
WHERE channel_type = 'WAC' 
AND is_active = true 
AND config::jsonb @> jsonb_build_object('wa_waba_id', $2::text)`

// UpdateChannelConfig updates the channel configuration
func (b *backend) UpdateChannelConfig(ctx context.Context, channel courier.Channel, config map[string]interface{}) error {
	// Convert config to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshaling channel config: %v", err)
	}

	// Update the channel config in the database
	_, err = b.db.ExecContext(ctx, updateChannelConfigSQL, configJSON, channel.UUID())
	if err != nil {
		return fmt.Errorf("error updating channel config: %v", err)
	}

	// Clear the channel from cache
	clearLocalChannel(channel.UUID())
	if channel.ChannelAddress() != courier.NilChannelAddress {
		clearLocalChannelByAddress(channel.ChannelAddress())
	}

	return nil
}

// UpdateChannelConfigByWabaID updates the channel configuration for all channels with matching waba_id
func (b *backend) UpdateChannelConfigByWabaID(ctx context.Context, wabaID string, configUpdates map[string]interface{}) error {
	// Convert config updates to JSON
	configJSON, err := json.Marshal(configUpdates)
	if err != nil {
		logrus.WithError(err).Error("[mmlite] error marshaling channel config updates")
		return fmt.Errorf("error marshaling channel config updates: %v", err)
	}

	logrus.WithField("waba_id", wabaID).Info("[mmlite] updating channel configs by waba_id")
	// Update all channels with matching waba_id
	result, err := b.db.ExecContext(ctx, updateChannelConfigByWabaIDSQL, configJSON, wabaID)
	if err != nil {
		logrus.WithError(err).Error("[mmlite] error updating channel configs by waba_id")
		return fmt.Errorf("error updating channel configs by waba_id: %v", err)
	}

	// Check how many rows were affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logrus.WithError(err).Error("[mmlite] error getting rows affected")
		return fmt.Errorf("error getting rows affected: %v", err)
	}

	if rowsAffected == 0 {
		logrus.WithField("waba_id", wabaID).Error("[mmlite] no channels found with waba_id")
		return fmt.Errorf("no channels found with waba_id: %s", wabaID)
	}

	logrus.WithField("waba_id", wabaID).WithField("rows_affected", rowsAffected).Info("[mmlite] channels updated with waba_id")
	return nil
}

// Config returns the channel's configuration
func (c *DBChannel) Config() map[string]interface{} {
	if !c.Config_.Valid {
		return make(map[string]interface{})
	}
	return c.Config_.Map
}
