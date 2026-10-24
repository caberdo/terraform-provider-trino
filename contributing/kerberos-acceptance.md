# Kerberos acceptance runbook

Kerberos cannot run in CI: it needs a KDC, a service keytab mounted into the
coordinator and an HTTPS endpoint. This runbook is the manual gate executed
before a release that touches authentication. It is deliberately copy-paste
ready; replace `EXAMPLE.COM` and the hostnames with your own and keep the realm
consistent everywhere.

The provider's Kerberos **configuration mapping and validation** are covered by
unit tests (`internal/provider/config_test.go`, `internal/trino/*_test.go`).
What this runbook proves is the end-to-end SPNEGO handshake against a real
coordinator: a plan, an apply, a read-back refresh and a destroy.

## 0. Prerequisites

- Trino 476 or newer, reachable over HTTPS.
- `krb5-kdc`, `krb5-admin-server`, `krb5-user` on the KDC host (Debian/Ubuntu
  package names).
- A DNS name for the coordinator (`trino.example.com`) that resolves from the
  machine running Terraform. The Kerberos service principal is host-based, so
  `localhost` does not work.
- A Java keystore for the coordinator's HTTPS listener.

Set the shared variables in every shell:

```bash
export REALM=EXAMPLE.COM
export KDC_HOST=kdc.example.com
export TRINO_HOST=trino.example.com
export TRINO_PORT=8443
export CLIENT_PRINCIPAL=alice
export KEYTAB=/etc/trino/trino.keytab
```

## 1. KDC: realm, principals and keytab

On the KDC host:

```bash
sudo kdb5_util create -s -r "$REALM"          # only once per realm
sudo kadmin.local -q "addprinc -randkey HTTP/$TRINO_HOST@$REALM"
sudo kadmin.local -q "addprinc -pw alice $CLIENT_PRINCIPAL@$REALM"
sudo kadmin.local -q "ktadd -k $KEYTAB HTTP/$TRINO_HOST@$REALM"
```

`HTTP` is the service name (the provider's `kerberos_service_name` default), not
`trino`. The Trino coordinator convention is
`HTTP/<host>@<REALM>`; using `trino/<host>` fails with
`Server not found in Kerberos database`.

The keytab must be readable by the Trino process and by nobody else:

```bash
sudo chown trino:trino "$KEYTAB"
sudo chmod 600 "$KEYTAB"
klist -kt "$KEYTAB"      # shows HTTP/trino.example.com@EXAMPLE.COM
```

On the client machine, point at the KDC in `/etc/krb5.conf`:

```ini
[libdefaults]
    default_realm = EXAMPLE.COM
    dns_lookup_kdc = false
    dns_lookup_realm = false
    rdns = false
    udp_preference_limit = 1

[realms]
    EXAMPLE.COM = {
        kdc = kdc.example.com
        admin_server = kdc.example.com
    }

[domain_realm]
    .example.com = EXAMPLE.COM
    example.com  = EXAMPLE.COM
```

Verify the client can authenticate before involving Trino:

```bash
kinit alice@EXAMPLE.COM
klist
```

The provider can use either a **keytab** or a **credential cache**, never both.
The examples below use the keytab; `kerberos_credential_cache = "FILE:/tmp/krb5cc_1000"`
is the alternative for an existing `kinit` session.

## 2. Trino: Kerberos + HTTPS

Generate the HTTPS keystore (self-signed is fine for the runbook; add the CA to
the provider's `ca_cert_file`):

```bash
keytool -genkeypair -alias trino -keyalg RSA -keysize 2048 -validity 365 \
  -dname "CN=$TRINO_HOST" -ext "SAN=dns:$TRINO_HOST" \
  -keystore /etc/trino/keystore.jks -storepass changeit -keypass changeit
```

`etc/config.properties` on the coordinator:

```properties
coordinator=true
node-scheduler.include-coordinator=true

http-server.http.enabled=false
http-server.https.enabled=true
http-server.https.port=8443
http-server.https.keystore.path=/etc/trino/keystore.jks
http-server.https.keystore.key=<keystore password>

http-server.authentication.type=KERBEROS
http-server.authentication.krb5.service-name=HTTP
http-server.authentication.krb5.principal-hostname=trino.example.com
http-server.authentication.krb5.keytab=/etc/trino/trino.keytab
http-server.authentication.krb5.config=/etc/krb5.conf
http-server.https.authenticated.port=8443

internal-communication.shared-secret=<random secret>
```

Start Trino and confirm the handshake outside Terraform:

```bash
curl -k --negotiate -u : https://trino.example.com:8443/v1/info
```

A 200 with JSON means SPNEGO works; a 401 means the service principal, keytab
permissions or DNS name is wrong.

## 3. Provider configuration

```hcl
terraform {
  required_providers {
    trino = {
      source = "caberdo/trino"
    }
  }
}

provider "trino" {
  endpoint    = "https://trino.example.com:8443"
  user        = "alice@EXAMPLE.COM"
  auth        = "kerberos"
  catalog     = "hive"
  schema      = "default"

  kerberos_principal    = "alice"
  kerberos_realm        = "EXAMPLE.COM"
  kerberos_keytab       = "/etc/trino/client.keytab"   # or kerberos_credential_cache
  kerberos_config_file  = "/etc/krb5.conf"
  kerberos_service_name = "HTTP"

  # The coordinator certificate is self-signed in this runbook.
  ca_cert_file = "/etc/trino/ca.pem"
}
```

The same values can come from `TRINO_*` environment variables, which is how the
manual run is usually driven:

```bash
export TRINO_ENDPOINT="https://trino.example.com:8443"
export TRINO_USER="alice@EXAMPLE.COM"
export TRINO_AUTH=kerberos
export TRINO_KERBEROS_PRINCIPAL=alice
export TRINO_KERBEROS_REALM=EXAMPLE.COM
export TRINO_KERBEROS_KEYTAB=/etc/trino/client.keytab
export TRINO_KERBEROS_CONFIG_FILE=/etc/krb5.conf
export TRINO_CA_CERT_FILE=/etc/trino/ca.pem
```

Client keytab for `alice`:

```bash
sudo kadmin.local -q "ktadd -k /etc/trino/client.keytab $CLIENT_PRINCIPAL@$REALM"
sudo chmod 600 /etc/trino/client.keytab
```

Validation the provider performs before dialling:

- `https` endpoint (Kerberos over plain HTTP is rejected);
- `kerberos_principal` and `kerberos_realm` present;
- keytab **xor** credential cache;
- the service principal pattern does not name a realm.

## 4. Expected outcome

```bash
terraform init
terraform plan      # creates trino_role.analyst, trino_grant_role.*, trino_schema.*
terraform apply     # exit 0; no credential appears in the output
terraform plan      # No changes.
terraform destroy   # exit 0; objects removed
```

Check the coordinator saw the authenticated user:

```sql
SELECT * FROM system.runtime.transactions;   -- no rows after destroy
SHOW ROLES FROM hive;                        -- empty after destroy
```

## 5. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Server not found in Kerberos database` | The service principal does not exist, or the KDC is looking in the wrong realm. | `kadmin.local -q "getprinc HTTP/trino.example.com@EXAMPLE.COM"`; re-run `ktadd`; confirm `default_realm` in `krb5.conf`. |
| `Server not found in Kerberos database` only from the client | DNS points at the wrong host, or `rdns` rewrites the name. | Use the canonical `trino.example.com`; set `rdns = false`; check `nslookup`. |
| `Clock skew too great` | KDC and client clocks drift beyond the 5-minute default. | Enable NTP/chrony on both; `kdestroy && kinit`; the provider cannot work around it. |
| `Pre-authentication information was invalid` | Wrong password, or a stale credential cache. | `kdestroy -A; kinit alice@EXAMPLE.COM`. |
| `GSS initiate failed` / `No valid credentials` | The keytab has the wrong principal or mode 644 permissions. | `klist -kt`; `chmod 600`; make sure `kerberos_principal` is the bare name, not `alice@EXAMPLE.COM`. |
| `401 Unauthorized` from `/v1/info` | Trino's `service-name` or `principal-hostname` does not match the principal. | Both must resolve to `HTTP/trino.example.com@EXAMPLE.COM`. |
| `x509: certificate signed by unknown authority` | The coordinator certificate is not trusted. | Set `ca_cert_file` to the CA; do not use `insecure_skip_verify` outside a test. |
| `kerberos_keytab` and `kerberos_credential_cache` conflict | Both are set. | Choose one; the provider rejects the pair deliberately. |

Record the date, Trino version and outcome in the release checklist. A runbook
that was not executed is an unverified release condition, not a pass.
