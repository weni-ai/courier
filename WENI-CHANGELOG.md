1.36.1
----------
 * External v2 support for send_url_template like a send_template

1.36.0
----------
 * Add external api v2 channel handler

1.35.0
----------
 * use MakeHTTPRequestWithRetry on weniwebchat handler to avoid request connection errors and other transitory errors

1.34.0
----------
 * Refactor billing message handling to simplify conditions and ensure status is included in billing messages

1.33.0
----------
 * Add typing action handling for Weni Webchat

1.32.1
----------
 * Add contact name field to billing Message struct

1.32.0
----------
 * Save data from click to whatsapp id

1.31.0
----------
 * Enhance WhatsApp message handling to support marketing templates

1.30.0
----------
 * Add status field to Message struct and update related functions

1.29.0
----------
 * Refactor model message sending logic to avoid unnecessary logging
 * Refactor metrics handling
 * Feat: s3 upload metric

1.28.0
----------
 * Implement template support in messaging system
 * Feat/wac demo

1.27.0
----------
 * Feat/prometheus
 * Enhance Instagram payload processing to ignore comments from the channel itself, preventing potential reply loops

1.26.0
----------
 * Add ActionSender interface and MsgActionType for message actions

1.25.0
----------
 * Adjust number management with the ninth digit for WhatsApp

1.24.0
----------
 * Feat: Save WAC context in Msg metadata
 * Feat: remove contact last seen write on sender

1.23.1
----------
 * Fix contact created with name as identity instead real name on WriteLastSeenOn

1.23.0
----------
 * Implement contact last seen feature

1.22.0
----------
 * add wenichats msg uuid that be sent to rmq exchange

1.21.0
----------
 * Add support for Instagram replies: comments, by tag and private reply

1.20.1
----------
 * Fix version

1.20.0
----------
 * Prevent contact duplication by check whatsapp variation before create

1.19.4
----------
 * Change product field name

1.19.3
----------
 * Fix webhook panic on value assertion failure

1.19.2
----------
 * Fix webhook panic on parse nil to string for get method request value

1.19.1
----------
 * Billing send message to exchange with routing keys instead directly to queue

1.19.0
----------
 * Feat: Add buttons support to whatsapp template message
 * Feat: Order details template

1.18.5
----------
 * Fix: document name on template message for whatsapp cloud

1.18.4
----------
 * Fix: Handle error for Whatsapp messages without text but with quick replies and attachments 

1.18.3
----------
 * Hotfix: MsgParts index out of range

1.18.2
----------
 * Feat: configuration for billing queue name on env var

1.18.1
----------
 * Fix: document name when message have quick replies on whatsapp handler
 * Fix: document name when message have quick replies on facebookapp handler for whatsapp cloud api
 * Fix: backslashes for headerText, footer and ctaMessage
 * Fix: handle empty flow data to not be sent as empty object in request

1.18.0
----------
 * Search for contact and update a Teams contact with the serviceURL

1.17.0
----------
 * Feat: Email Channel Handler

1.16.0
----------
 * Upload documents to telegram
 * Feat: WhatsApp order details

1.15.0
----------
 * Add button support with url in WA

1.14.2
----------
 * Fix: Billing verification

1.14.1
----------
 * Fix: Billing queue

1.14.0
----------
 * Feat: Add support to WhatsApp Flows
 * Add support for flow messaging webhooks

1.13.0-courier-7.1.0
----------
 * Add support for interactive messages in WA

1.12.2-courier-7.1.0
----------
 * Extra cases for escape telegram markdown text messages (odd * cases).
 * Handle webhook null headers

1.12.1-courier-7.1.0
----------
 * Extra cases for escape telegram markdown text messages

1.12.0-courier-7.1.0
----------
 * Convert response_json from flow messages to json

1.11.1-courier-7.1.0
----------
 * Channel cache ttl from 1min to 5min
 * Fix flush status file for status D and V without sent on datetime

1.11.0-courier-7.1.0
----------
 * Fix local channel cache by address
 * Lock subsequent load channel from db to best use of local cache

1.10.0-courier-7.1.0
----------
 * Support for CTA message for whatsapp cloud channels

1.9.5-courier-7.1.0
----------
 * Escape telegram text messages for markdown parse mode requirements

1.9.4-courier-7.1.0
----------
 * Add telegram Markdown legacy support

1.9.3-courier-7.1.0
----------
 * Add 'S' status to updateMsgID query

1.9.2-courier-7.1.0
----------
 * Remove updates for last_seen_on
 * Remove telegram parse_mode

1.9.1-courier-7.1.0
----------
 * Add telegram parse_mode channel config for formmating text using MarkdownV2 as default

1.9.0-courier-7.1.0
----------
 * Add support for WhatsApp message sending card

1.8.1-courier-7.1.0
----------
 * Billing integration with retry and reconnect capabilities

1.8.0-courier-7.1.0
----------
 * Billing integration for all channel types, both for sending and receiving messages

1.7.3-courier-7.1.0
----------
 * Add text size check for audio and sticker

1.7.2-courier-7.1.0
----------
 * Add channel uuid attribute on weni webchat mopayload

1.7.1-courier-7.1.0
----------
 * Fix to truncate section titles to 24 characters

1.7.0-courier-7.1.0
----------
 * Send template update webhooks to Integrations

1.6.3-courier-7.1.0
----------
 * Defer db connection close on health check

1.6.2-courier-7.1.0
----------
 * Remove line break from end of text for TM
 * Add Billing integration for WAC channels with rabbitmq 

1.6.1-courier-7.1.0
----------
 * /health do health check for redis, database, sentry and s3

1.6.0-courier-7.1.0
----------
  * Support sending catalog messages in WA

1.5.3-courier-7.1.0
----------
  * Fix handling of message responses in Teams

1.5.2-courier-7.1.0
----------
  * Fix handling of text attachments for teams

1.5.1-courier-7.1.0
----------
  * Send image link in message text to Teams

1.5.0-courier-7.1.0
----------
  * Change product list structure to preserve insertion order

1.4.39-courier-7.1.0
----------
  * Fix limitation of product sections

1.4.38-courier-7.1.0
----------
  * Add nfm_reply in metadata

1.4.37-courier-7.1.0
----------
  * Use userAccessToken to send WAC messages

1.4.36-courier-7.1.0
----------
  * WAC channels update last seen on when receive callback status delivered or read

1.4.35-courier-7.1.0
----------
  * Send attachment link in Teams 

1.4.34-courier-7.1.0
----------
  * Fix sending messages with attachments and no captions

1.4.33-courier-7.1.0
----------
  * Divide searches into product sections

1.4.32-courier-7.1.0
----------
  * Fix attachment handling for Teams handler

1.4.31-courier-7.1.0
----------
  * Add other location fields in the message

1.4.30-courier-7.1.0
----------
  * Msg catalog implementations in msg and handler

1.4.29-courier-7.1.0
----------
  * Normalize strings with slashes in quick replies on wwc

1.4.28-courier-7.1.0
----------
  * Fix attFormat variable setting for WAC and WA

1.4.27-courier-7.1.0
----------
  * Add support for sending sticker to WA and WAC

1.4.26-courier-7.1.0
----------
  * Add healthcheck endpoint at c/health

1.4.25-courier-7.1.0
----------
  * Add 'V' status to check definition of sent_on

1.4.24-courier-7.1.0
----------
  * Fix Order WAC types

1.4.23-courier-7.1.0
----------
  * Support for Order in WAC

1.4.22-courier-7.1.0
----------
  * Increase test coverage in facebookapp handler

1.4.21-courier-7.1.0
----------
  * Quick Response Support and Contact Email Recovery in Teams

1.4.20-courier-7.1.0
----------
  * Support for Referrals in WA

1.4.19-courier-7.1.0
----------
  * Support for referral messages in WAC

1.4.18-courier-7.1.0
----------
  * Support send audio with text in WA 

1.4.17-courier-7.1.0
----------
  * Cache media ids for WhatsApp cloud attachments

1.4.16-courier-7.1.0
----------
  * Fix test TestMsgSuite/TestWriteAttachment

1.4.15-courier-7.1.0
----------
  * Improve URL verification for webhooks

1.4.14-courier-7.1.0
----------
  * Add support for receiving contact type messages in WAC

1.4.13-courier-7.1.0
----------
  * Use user token to download files from Slack
  * Fix maximum message size limits for WAC, FBA and IG
  * Use the new package to find out the mime type of attachments

1.4.12-courier-7.1.0
----------
  * Set Wait media channels config to empty array by default

1.4.11-courier-7.1.0
----------
  * Fix media ordenation on wait previous msg be delivered on configured channels

1.4.10-courier-7.1.0
----------
  * Add module to send webhooks for WAC and WA #2
  * Add read status for WAC and WA #3

1.4.9-courier-7.1.0
----------
  * Add logic in sender to wait previous media msg be delivered to send current msg for some channels

1.4.8-courier-7.1.0
----------
  * Fix word 'menu' in Arabic for list messages #141

1.4.7-courier-7.1.0
----------
  * Add "Menu" word translation mapping to list messages in WAC and WA channels #139

1.4.6-courier-7.1.0
----------
  * Normalize quick response strings with slashes for TG and WA channels #137
  * Fix receiving multiple media for TG, WAC and WA channels #136

1.4.5-courier-7.1.0
----------
  * Remove expiration_timestamp from moPayload in WAC #133

1.4.4-courier-7.1.0
----------
  * Add support for sending captioned attachments in WAC #131
 
1.4.3-courier-7.1.0
----------
  * Quick Replies support in the Slack handler #129

1.4.2-courier-7.1.0
----------
  * Fix URL of attachments in WAC handler #127


1.4.1-courier-7.1.0
----------  
  * Fix receiving attachments and quick replies

1.4.0-courier-7.1.0
----------  
  * Integration support with Microsoft Teams

1.3.3-courier-7.1.0
----------  
  * Media message template support, link preview and document name correction on WhatsApp Cloud #118

1.3.2-courier-7.1.0
----------
  * Fix to prevent create a new contact without extra 9 in wpp number, instead, updating if already has one with the extra 9, handled in whatsapp cloud channels #119

1.3.1-courier-7.1.0
----------
  * Fix to ensure update last_seen_on if there is no error and no failure to send the message.

1.3.0-courier-7.1.0
----------
  * Slack Bot Channel Handler
  * Whatsapp Cloud Handler

1.2.1-courier-7.1.0
----------
  * Update contact last_seen_on on send message to him

1.2.0-courier-7.1.0
----------
  * Merge tag v7.1.0 from nyaruka into our 1.1.8-courier-7.0.0

1.1.8-courier-7.0.0
----------
 * Fix whatsapp handler to update the contact URN if the wa_id returned in the send message request is different from the current URN path, avoiding creating a new contact.

1.1.7-courier-7.0.0
----------
 * Add library with greater support for detection of mime types in Whatsapp

1.1.6-courier-7.0.0
----------
 * Support for viewing sent links in Whatsapp messages

1.1.5-courier-7.0.0
----------
 * Fix sending document names in whatsapp media message templates

1.1.4-courier-7.0.0
----------
 * Add Kyrgyzstan language support in whatsapp templates

1.1.3-courier-7.0.0
----------
 * fix whatsapp uploaded attachment file name

1.1.2-courier-7.0.0
----------
 * Fix metadata fetching for new Facebook contacts

1.1.1-courier-7.0.0
----------
 * Add Instagram Handler
 * Update gocommon to v1.16.2

1.1.0-courier-7.0.0
----------
 * Fix: Gujarati whatsapp language code
 * add button layout support on viber channel

1.0.0-courier-7.0.0
----------
 * Update Dockerfile to go 1.17.5 
 * Support to facebook customer feedback template
 * Support whatsapp media message template
 * Fix to prevent requests from blocked contact generate channel log
 * Weni-Webchat handler
 * Support to build Docker image
