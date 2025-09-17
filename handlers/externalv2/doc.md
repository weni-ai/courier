# Documentação do Canal ExternalV2

## Visão Geral

O canal ExternalV2 (tipo `E2`) é um handler flexível que permite integração com sistemas externos de mensageria através de templates configuráveis. Este canal oferece alta customização para envio e recebimento de mensagens via HTTP.

## Características Principais

- ✅ Templates customizáveis para envio e recebimento
- ✅ Suporte a JSON e form-urlencoded
- ✅ Envio de anexos em partes separadas (opcional)
- ✅ Status de entrega (sent, delivered, failed)
- ✅ Suporte a stop contacts
- ✅ Autenticação configurável
- ✅ Funções auxiliares nos templates

## Configurações do Canal

### Configurações Obrigatórias

| Configuração | Chave | Descrição |
|-------------|--------|-----------|
| URL de Envio | `send_url` | URL para envio de mensagens |
| Template de Envio | `send_template` | Template JSON para formatar dados de envio |
| Template de Recebimento | `receive_template` | Template para mapear dados recebidos |

### Configurações Opcionais

| Configuração | Chave | Padrão | Descrição |
|-------------|-------|---------|-----------|
| URL de Mídia | `send_media_url` | `send_url` | URL específica para envio de mídias |
| Método HTTP | `send_method` | `POST` | Método HTTP para envio |
| Tipo de Conteúdo | `content_type` | `urlencoded` | `json` ou `urlencoded` |
| Autorização | `send_authorization` | - | Header de autorização |
| Verificação de Resposta | `mt_response_check` | - | String para validar resposta |
| Resposta MO | `mo_response` | - | Resposta customizada para mensagens recebidas |
| Tipo de Resposta MO | `mo_response_content_type` | - | Content-type da resposta MO |
| Anexos em Partes | `send_attachment_in_parts` | `false` | Enviar anexos separadamente |

## Templates de Envio

### Estrutura do Template

O template de envio recebe um objeto com todas as informações da mensagem:

```json
{
  "id": "123456789",
  "uuid": "550e8400-e29b-41d4-a716-446655440000",
  "text": "Olá, como você está?",
  "attachments": ["image:https://exemplo.com/foto.jpg"],
  "contact": "+5511999999999",
  "urn": {
    "scheme": "tel",
    "path": "+5511999999999",
    "query": {},
    "fragment": ""
  },
  "urn_auth": "token_auth_opcional",
  "channel": "12345",
  "channel_uuid": "channel-uuid-here",
  "quick_replies": ["Sim", "Não"],
  "products": [],
  "header": "Cabeçalho da mensagem",
  "body": "Corpo da mensagem",
  "footer": "Rodapé da mensagem",
  "action": "send_message",
  "send_catalog": false,
  "header_type": "text",
  "header_text": "Texto do cabeçalho",
  "list_message": {},
  "interaction_type": "text",
  "cta_message": {},
  "flow_message": {},
  "order_details_message": {},
  "buttons": [],
  "action_type": "send"
}
```

### Exemplo de Template de Envio

#### Para API JSON:
```json
{
  "to": "{{.contact}}",
  "message": "{{.text}}",
  "media": {{if .attachments}}"{{index .attachments 0}}"{{else}}null{{end}},
  "message_id": "{{.id}}"
}
```

#### Para API Form-Encoded:
```json
{
  "phone": "{{.contact}}",
  "text": "{{.text}}",
  "msg_id": "{{.id}}"
}
```

### Funções Auxiliares nos Templates

| Função | Descrição | Exemplo |
|--------|-----------|---------|
| `split` | Divide string | `{{split .text " "}}` |
| `attType` | Tipo do anexo | `{{attType .attachment}}` retorna "image" |
| `attURL` | URL do anexo | `{{attURL .attachment}}` retorna a URL |

### Exemplo com Anexos:
```json
{
  "to": "{{.contact}}",
  "message": "{{.text}}",
  {{if .attachments}}
  "attachments": [
    {{range $i, $att := .attachments}}
    {{if $i}},{{end}}
    {
      "type": "{{attType $att}}",
      "url": "{{attURL $att}}"
    }
    {{end}}
  ]
  {{end}}
}
```

## Templates de Recebimento

### Estrutura Esperada

O template de recebimento deve mapear os dados recebidos para o formato esperado pelo Courier:

```json
{
  "messages": [
    {
      "id": "msg_id_externo",
      "urn_identity": "+5511999999999",
      "urn_auth": "token_opcional",
      "contact_name": "Nome do Contato",
      "date": "2023-12-01T10:30:00Z",
      "text": "Mensagem recebida",
      "attachments": ["image:https://exemplo.com/foto.jpg"]
    }
  ]
}
```

### Exemplo de Template de Recebimento

Para webhook que recebe dados como:
```json
{
  "from": "+5511999999999",
  "body": "Olá!",
  "id": "ext_msg_123"
}
```

Template de mapeamento:
```json
{
  "messages": [
    {
      "id": "{{.id}}",
      "urn_identity": "{{.from}}",
      "text": "{{.body}}",
    }
  ]
}
```

### Exemplo com Múltiplas Mensagens:
podem ter canais que enviam mensagens multiplas com uma única requisição então é possível tratar com esse padrão.

```json
{
  "messages": [
    {{range $i, $msg := .messages}}
    {{if $i}},{{end}}
    {
      "id": "{{$msg.id}}",
      "urn_identity": "{{$msg.from}}",
      "text": "{{$msg.text}}",
      "attachments": [{{range $j, $att := $msg.media}}{{if $j}},{{end}}"{{$att}}"{{end}}]
    }
    {{end}}
  ]
}
```

## Endpoints do Canal

### Recebimento de Mensagens
- **URL**: `/c/e2/{uuid}/receive`
- **Métodos**: `GET`, `POST`
- **Content-Type**: `application/json` ou `multipart/form-data`

### Status de Mensagem
- **Enviada**: `/c/e2/{uuid}/sent?id={msg_id}`
- **Entregue**: `/c/e2/{uuid}/delivered?id={msg_id}`
- **Falhada**: `/c/e2/{uuid}/failed?id={msg_id}`
- **Métodos**: `GET`, `POST`

### Stop Contact
- **URL**: `/c/e2/{uuid}/stopped`
- **Métodos**: `GET`, `POST`
- **Parâmetro**: `from` (número/identificador do contato)

## Exemplos de Configuração

### Exemplo 1: API JSON Simples

```json
{
  "send_url": "https://api.exemplo.com/send",
  "send_method": "POST",
  "content_type": "json",
  "send_authorization": "Bearer seu_token_aqui",
  "send_template": "{\"to\": \"{{.contact}}\", \"message\": \"{{.text}}\", \"id\": \"{{.id}}\"}",
  "receive_template": "{\"messages\": [{\"id\": \"{{.messageId}}\", \"urn_identity\": \"{{.from}}\", \"text\": \"{{.text}}\"}]}",
  "mt_response_check": "success"
}
```

### Exemplo 2: API Form-Encoded

```json
{
  "send_url": "https://gateway.exemplo.com/sms",
  "send_method": "POST", 
  "content_type": "urlencoded",
  "send_template": "{\"phone\": \"{{.contact}}\", \"message\": \"{{.text}}\", \"api_key\": \"sua_chave\"}",
  "receive_template": "{\"messages\": [{\"id\": \"{{.id}}\", \"urn_identity\": \"{{.sender}}\", \"text\": \"{{.message}}\"}]}"
}
```

### Exemplo 3: Com Anexos Separados

```json
{
  "send_url": "https://api.exemplo.com/text",
  "send_media_url": "https://api.exemplo.com/media",
  "send_attachment_in_parts": "true",
  "content_type": "json",
  "send_template": "{\"to\": \"{{.contact}}\", \"content\": \"{{.text}}\", {{if .attachments}}\"media\": \"{{index .attachments 0}}\"{{end}}}",
  "receive_template": "{\"messages\": [{\"id\": \"{{.msg_id}}\", \"urn_identity\": \"{{.from}}\", \"text\": \"{{.text}}\", \"attachments\": [{{range $i, $att := .media}}{{if $i}},{{end}}\"{{$att}}\"{{end}}]}]}"
}
```

## Tratamento de Erros

### Erros Comuns

1. **Template vazio**: "receive body template is empty"
2. **Template inválido**: "unable to parse receive body template"
3. **JSON inválido**: "unable to decode request body"
4. **Content-type não suportado**: "unsupported content type"
5. **Resposta inválida**: "received invalid response content"

### Debugging

1. Verifique os logs do canal para ver requisições e respostas
2. Teste templates em ferramentas online de Go templates
3. Valide JSON usando ferramentas de validação
4. Confirme que URLs estão acessíveis

## Boas Práticas

### Templates
- Use aspas duplas para strings JSON
- Escape caracteres especiais quando necessário
- Teste templates com dados reais antes da produção
- Use funções auxiliares para formatação

### Configuração
- Configure `mt_response_check` para validar respostas
- Use URLs HTTPS em produção
- Configure timeouts adequados
- Implemente retry logic no sistema externo

### Monitoramento
- Configure logs detalhados
- Monitore status de entrega
- Implemente alertas para falhas
- Acompanhe métricas de performance

### Segurança
- Use autenticação forte (tokens, certificados)
- Valide dados de entrada
- Configure rate limiting
- Use HTTPS para todas as comunicações

## Casos de Uso

### 1. Gateway SMS Tradicional
Para integração com gateways SMS que usam HTTP simples.

### 2. APIs WhatsApp Business
Para conectar com provedores de WhatsApp Business API.

### 3. Sistemas de Chat Próprios
Para integrar com sistemas de chat desenvolvidos internamente.

### 4. Agregadores de Mensagem
Para conectar com plataformas que agregam múltiplos canais.


## Exemplo de integração real de um bot do telegram para o External v2 (parte essencial da doc) 

- No shell do flows rode as instruções de acordo com o seguinte padrão:

```python
from temba.channels.models import Channel
from temba.orgs.models import Org
from django.contrib.auth.models import User

user = User.objects.get(email="rafael.soares@weni.ai")

org = Org.objects.get(proj_uuid="fa147fa6-5af0-4d99-9c00-043c89d97392")

config = {
	"mo_response_content_type": "application/json",
	"mo_response": "",
	"mt_response_check": "",
	"send_url": "https://api.telegram.org/bot5311126581:AAGpycuyyZOTyUW2L-P7lMoTOy86sWugdDk/sendMessage",
	"send_media_url": "https://api.telegram.org/bot5311126581:AAGpycuyyZOTyUW2L-P7lMoTOy86sWugdDk/sendPhoto",
	"send_method": "POST",
	"send_template": "{\"chat_id\":\"{{.contact}}\",\"text\":\"{{.text}}\",\"parse_mode\":\"Markdown\"}",
	"content_type": "application/x-www-form-urlencoded",
	"receive_template": "{\"messages\":[{\"urn_identity\":\"{{.message.from.id}}\",\"text\":\"{{.message.text}}\",\"contact_name\":\"{{.message.from.username}}\",\"id\":\"{{.message.message_id}}\"}]}",
	"send_authorization": ""
}

channel = Channel.create(
	org, # organização
	user, # usuário que está criando o canal
	None, # país (pode ser None)
	'E2', # E2 é o tipo external channel v2
	name='Ex2 telegram',
	address='telegramex2',
	config=config,
	role=Channel.ROLE_SEND + Channel.ROLE_RECEIVE, # valor padrão de roles
	schemes=['telegram'], # se for uma appi qualquer pode ser external
)

print(channel.uuid) ## obtenha o uuid para poder configurar o webhook de recebimento
```

- configure o webhook do canal:

```
curl -F "url=https://flows.stg.cloud.weni.ai/c/e2/2d0c3708-247e-419d-85b0-75110771e041/receive" https://api.telegram.org/bot5311126581:AAGpycuyyZOTyUW2L-P7lMoTOy86sWugdDk/setWebhook
```

pronto seu canal do telegram estará funcionando.

obs: substitua os valores adequadamente.

## Migração de External (v1)

Se você está migrando do canal External original:

1. **URL**: Mude de `/ex/` para `/e2/`
2. **Templates**: Adapte para o novo formato de templates
3. **Configuração**: Use as novas chaves de configuração
4. **Teste**: Valide todos os fluxos antes da produção

---

**Nota**: Esta documentação cobre a implementação atual do ExternalV2. Para dúvidas específicas ou casos de uso especiais, consulte o código fonte ou entre em contato com a equipe engine, rafael.soares@vtex.com e matheus.soares@vtex.com.
