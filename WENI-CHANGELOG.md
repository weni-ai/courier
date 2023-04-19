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
