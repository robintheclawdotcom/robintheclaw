use actix_web::http::header::HeaderMap;
use anyhow::{anyhow, Result};
use chrono::{DateTime, Duration, TimeZone, Utc};
use hmac::{Hmac, Mac};
use sha2::{Digest, Sha256};

type HmacSha256 = Hmac<Sha256>;

#[derive(Clone)]
pub struct ServiceAuth {
    caller: Option<String>,
    key: Option<[u8; 32]>,
}

pub struct AuthorizedRequest {
    pub caller: String,
    pub nonce: String,
    pub nonce_expires_at: DateTime<Utc>,
}

impl ServiceAuth {
    pub fn new(caller: &str, key_hex: &str) -> Result<Self> {
        if caller.trim().is_empty() && key_hex.trim().is_empty() {
            return Ok(Self {
                caller: None,
                key: None,
            });
        }
        if caller.len() < 3
            || caller.len() > 64
            || !caller
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        {
            return Err(anyhow!("invalid readiness publisher caller id"));
        }
        let key: [u8; 32] = hex::decode(key_hex)
            .map_err(|_| anyhow!("invalid readiness publisher HMAC key"))?
            .try_into()
            .map_err(|_| anyhow!("invalid readiness publisher HMAC key"))?;
        Ok(Self {
            caller: Some(caller.to_string()),
            key: Some(key),
        })
    }

    pub fn is_enabled(&self) -> bool {
        self.caller.is_some() && self.key.is_some()
    }

    pub fn authorize(
        &self,
        method: &str,
        path: &str,
        headers: &HeaderMap,
        body: &[u8],
        now: DateTime<Utc>,
    ) -> Result<AuthorizedRequest> {
        let expected_caller = self
            .caller
            .as_deref()
            .ok_or_else(|| anyhow!("readiness publisher authentication is disabled"))?;
        let key = self
            .key
            .as_ref()
            .ok_or_else(|| anyhow!("readiness publisher authentication is disabled"))?;
        let caller = header(headers, "X-RTC-Caller")?;
        if caller != expected_caller {
            return Err(anyhow!("invalid readiness publisher authentication"));
        }
        let timestamp_text = header(headers, "X-RTC-Timestamp")?;
        let timestamp = timestamp_text
            .parse::<i64>()
            .ok()
            .and_then(|value| Utc.timestamp_opt(value, 0).single())
            .ok_or_else(|| anyhow!("invalid readiness publisher authentication"))?;
        if timestamp < now - Duration::seconds(30) || timestamp > now + Duration::seconds(30) {
            return Err(anyhow!("invalid readiness publisher authentication"));
        }
        let nonce = header(headers, "X-RTC-Nonce")?;
        if nonce.len() < 32
            || nonce.len() > 128
            || !nonce
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
        {
            return Err(anyhow!("invalid readiness publisher authentication"));
        }
        let signature = hex::decode(header(headers, "X-RTC-Signature")?)
            .map_err(|_| anyhow!("invalid readiness publisher authentication"))?;
        let body_digest = Sha256::digest(body);
        let canonical = format!(
            "{method}\n{path}\n{caller}\n{timestamp_text}\n{nonce}\n{}",
            hex::encode(body_digest)
        );
        let mut mac = HmacSha256::new_from_slice(key)
            .map_err(|_| anyhow!("invalid readiness publisher authentication"))?;
        mac.update(canonical.as_bytes());
        mac.verify_slice(&signature)
            .map_err(|_| anyhow!("invalid readiness publisher authentication"))?;
        Ok(AuthorizedRequest {
            caller: caller.to_string(),
            nonce: nonce.to_string(),
            nonce_expires_at: now + Duration::minutes(1),
        })
    }
}

fn header<'a>(headers: &'a HeaderMap, name: &str) -> Result<&'a str> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| anyhow!("invalid readiness publisher authentication"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_partial_configuration() {
        assert!(ServiceAuth::new("readiness-publisher", "00").is_err());
        assert!(ServiceAuth::new("", &"00".repeat(32)).is_err());
        assert!(!ServiceAuth::new("", "").unwrap().is_enabled());
    }
}
