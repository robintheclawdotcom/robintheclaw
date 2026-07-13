use std::{env, net::SocketAddr, str::FromStr};
use thiserror::Error;

#[derive(Clone)]
pub struct Config {
    pub enabled: bool,
    pub listen: SocketAddr,
    pub database_url: Option<String>,
    pub api_token: Option<String>,
    pub lighter_signer_url: Option<String>,
    pub robinhood_signer_url: Option<String>,
    pub signer_caller_id: Option<String>,
    pub lighter_signer_hmac_key: Option<[u8; 32]>,
    pub robinhood_signer_hmac_key: Option<[u8; 32]>,
    pub lighter_api_url: Option<String>,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum ConfigError {
    #[error("LISTEN_ADDRESS is invalid")]
    InvalidListen,
    #[error("{0} is required when execution is enabled")]
    Missing(&'static str),
    #[error("COORDINATOR_API_TOKEN must contain at least 32 bytes")]
    WeakToken,
    #[error("signer URLs must use HTTPS or Render private-network HTTP")]
    InvalidSignerUrl,
    #[error("signer HMAC keys must be 32-byte hex values")]
    InvalidHmacKey,
    #[error("SIGNER_CALLER_ID must be a lowercase service identifier")]
    InvalidCallerId,
    #[error("LIGHTER_API_URL must be an official HTTPS endpoint")]
    InvalidLighterApiUrl,
}

impl Config {
    pub fn from_env() -> Result<Self, ConfigError> {
        let enabled =
            env::var("COORDINATOR_ENABLED").is_ok_and(|value| value.eq_ignore_ascii_case("true"));
        let listen = env::var("LISTEN_ADDRESS")
            .unwrap_or_else(|_| "0.0.0.0:8080".into())
            .parse()
            .map_err(|_| ConfigError::InvalidListen)?;
        if !enabled {
            return Ok(Self {
                enabled,
                listen,
                database_url: None,
                api_token: None,
                lighter_signer_url: None,
                robinhood_signer_url: None,
                signer_caller_id: None,
                lighter_signer_hmac_key: None,
                robinhood_signer_hmac_key: None,
                lighter_api_url: None,
            });
        }
        let config = Self {
            enabled,
            listen,
            database_url: env::var("DATABASE_URL").ok(),
            api_token: env::var("COORDINATOR_API_TOKEN").ok(),
            lighter_signer_url: signer_url("LIGHTER_SIGNER_URL", "LIGHTER_SIGNER_HOSTPORT")?,
            robinhood_signer_url: signer_url("ROBINHOOD_SIGNER_URL", "ROBINHOOD_SIGNER_HOSTPORT")?,
            signer_caller_id: env::var("SIGNER_CALLER_ID").ok(),
            lighter_signer_hmac_key: hmac_key("LIGHTER_SIGNER_HMAC_KEY")?,
            robinhood_signer_hmac_key: hmac_key("ROBINHOOD_SIGNER_HMAC_KEY")?,
            lighter_api_url: env::var("LIGHTER_API_URL").ok(),
        };
        config.validate()?;
        Ok(config)
    }

    pub fn validate(&self) -> Result<(), ConfigError> {
        if !self.enabled {
            return Ok(());
        }
        let required = [
            (self.database_url.as_deref(), "DATABASE_URL"),
            (self.api_token.as_deref(), "COORDINATOR_API_TOKEN"),
            (self.lighter_signer_url.as_deref(), "LIGHTER_SIGNER_URL"),
            (self.robinhood_signer_url.as_deref(), "ROBINHOOD_SIGNER_URL"),
            (self.signer_caller_id.as_deref(), "SIGNER_CALLER_ID"),
            (self.lighter_api_url.as_deref(), "LIGHTER_API_URL"),
        ];
        for (value, name) in required {
            if value.is_none_or(str::is_empty) {
                return Err(ConfigError::Missing(name));
            }
        }
        if self
            .api_token
            .as_deref()
            .is_none_or(|value| value.len() < 32)
        {
            return Err(ConfigError::WeakToken);
        }
        for url in [
            self.lighter_signer_url.as_deref().unwrap(),
            self.robinhood_signer_url.as_deref().unwrap(),
        ] {
            if !(url.starts_with("https://") || is_private_http(url)) {
                return Err(ConfigError::InvalidSignerUrl);
            }
        }
        if self.lighter_signer_hmac_key.is_none() || self.robinhood_signer_hmac_key.is_none() {
            return Err(ConfigError::InvalidHmacKey);
        }
        if self
            .signer_caller_id
            .as_deref()
            .is_none_or(|value| !valid_caller_id(value))
        {
            return Err(ConfigError::InvalidCallerId);
        }
        if self
            .lighter_api_url
            .as_deref()
            .is_none_or(|value| !is_official_lighter_api(value))
        {
            return Err(ConfigError::InvalidLighterApiUrl);
        }
        Ok(())
    }
}

fn hmac_key(name: &'static str) -> Result<Option<[u8; 32]>, ConfigError> {
    let Ok(encoded) = env::var(name) else {
        return Ok(None);
    };
    let decoded = hex::decode(encoded).map_err(|_| ConfigError::InvalidHmacKey)?;
    decoded
        .try_into()
        .map(Some)
        .map_err(|_| ConfigError::InvalidHmacKey)
}

fn valid_caller_id(value: &str) -> bool {
    (3..=64).contains(&value.len())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
}

fn is_official_lighter_api(value: &str) -> bool {
    matches!(
        value.trim_end_matches('/'),
        "https://mainnet.zklighter.elliot.ai" | "https://testnet.zklighter.elliot.ai"
    )
}

fn is_private_http(value: &str) -> bool {
    let Some(authority) = value.strip_prefix("http://") else {
        return false;
    };
    let host = authority.split(['/', ':']).next().unwrap_or_default();
    host == "localhost"
        || host == "127.0.0.1"
        || host.ends_with(".internal")
        || std::net::IpAddr::from_str(host).is_ok_and(|address| match address {
            std::net::IpAddr::V4(address) => address.is_private() || address.is_loopback(),
            std::net::IpAddr::V6(address) => address.is_loopback() || address.is_unique_local(),
        })
}

fn signer_url(
    url_key: &'static str,
    hostport_key: &'static str,
) -> Result<Option<String>, ConfigError> {
    if let Ok(value) = env::var(url_key) {
        return Ok(Some(value));
    }
    let Ok(hostport) = env::var(hostport_key) else {
        return Ok(None);
    };
    private_hostport_url(&hostport).map(Some)
}

fn private_hostport_url(hostport: &str) -> Result<String, ConfigError> {
    let Some((host, port)) = hostport.rsplit_once(':') else {
        return Err(ConfigError::InvalidSignerUrl);
    };
    let port = port
        .parse::<u16>()
        .map_err(|_| ConfigError::InvalidSignerUrl)?;
    if host.is_empty()
        || !host
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        || port == 0
    {
        return Err(ConfigError::InvalidSignerUrl);
    }
    Ok(format!("http://{hostport}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn enabled() -> Config {
        Config {
            enabled: true,
            listen: "127.0.0.1:8080".parse().unwrap(),
            database_url: Some("postgres://db".into()),
            api_token: Some("a".repeat(32)),
            lighter_signer_url: Some("http://lighter.internal:8080".into()),
            robinhood_signer_url: Some("https://signer.example".into()),
            signer_caller_id: Some("execution-coordinator".into()),
            lighter_signer_hmac_key: Some([1; 32]),
            robinhood_signer_hmac_key: Some([2; 32]),
            lighter_api_url: Some("https://mainnet.zklighter.elliot.ai".into()),
        }
    }

    #[test]
    fn disabled_configuration_has_no_dependencies() {
        let mut config = enabled();
        config.enabled = false;
        config.database_url = None;
        config.api_token = None;
        config.lighter_signer_url = None;
        config.robinhood_signer_url = None;
        config.signer_caller_id = None;
        config.lighter_signer_hmac_key = None;
        config.robinhood_signer_hmac_key = None;
        config.lighter_api_url = None;
        assert_eq!(config.validate(), Ok(()));
    }

    #[test]
    fn public_plaintext_signer_is_rejected() {
        let mut config = enabled();
        config.lighter_signer_url = Some("http://public.example".into());
        assert_eq!(config.validate(), Err(ConfigError::InvalidSignerUrl));
    }

    #[test]
    fn render_private_hostport_is_accepted() {
        assert_eq!(
            private_hostport_url("robin-lighter-signer:10000"),
            Ok("http://robin-lighter-signer:10000".into())
        );
        assert_eq!(
            private_hostport_url("public.example:10000"),
            Err(ConfigError::InvalidSignerUrl)
        );
        assert_eq!(
            private_hostport_url("robin-lighter-signer:0"),
            Err(ConfigError::InvalidSignerUrl)
        );
    }

    #[test]
    fn unofficial_lighter_endpoint_is_rejected() {
        let mut config = enabled();
        config.lighter_api_url = Some("https://lighter.invalid".into());
        assert_eq!(config.validate(), Err(ConfigError::InvalidLighterApiUrl));
    }
}
