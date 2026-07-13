use crate::state::AppState;
use actix_web::HttpRequest;
use jsonwebtoken::{decode, Algorithm, DecodingKey, Validation};
use serde::Deserialize;

#[derive(Clone)]
pub struct AuthService {
    app_id: Option<String>,
    key: Option<DecodingKey>,
}

#[derive(Clone, Debug)]
pub struct AuthenticatedUser {
    pub did: String,
    pub session_id: String,
}

#[derive(Debug, Deserialize)]
struct Claims {
    sub: String,
    sid: String,
    iss: String,
    aud: String,
    exp: usize,
}

#[derive(Debug)]
pub enum AuthError {
    Missing,
    Unavailable,
    Invalid,
}

impl AuthService {
    pub fn new(app_id: Option<String>, verification_key: Option<String>) -> Self {
        let key = verification_key
            .as_deref()
            .and_then(|pem| DecodingKey::from_ec_pem(pem.as_bytes()).ok());
        Self { app_id, key }
    }

    pub fn authenticate(&self, req: &HttpRequest) -> Result<AuthenticatedUser, AuthError> {
        let token = token_from_request(req).ok_or(AuthError::Missing)?;
        let app_id = self.app_id.as_deref().ok_or(AuthError::Unavailable)?;
        let key = self.key.as_ref().ok_or(AuthError::Unavailable)?;

        let mut validation = Validation::new(Algorithm::ES256);
        validation.set_audience(&[app_id]);
        validation.set_issuer(&["privy.io"]);
        validation.validate_exp = true;

        let claims = decode::<Claims>(&token, key, &validation)
            .map_err(|_| AuthError::Invalid)?
            .claims;
        if claims.sub.is_empty()
            || claims.sid.is_empty()
            || claims.iss != "privy.io"
            || claims.aud != app_id
            || claims.exp == 0
        {
            return Err(AuthError::Invalid);
        }

        Ok(AuthenticatedUser {
            did: claims.sub,
            session_id: claims.sid,
        })
    }
}

pub fn require_user(req: &HttpRequest, state: &AppState) -> Result<AuthenticatedUser, AuthError> {
    state.auth.authenticate(req)
}

fn token_from_request(req: &HttpRequest) -> Option<String> {
    if let Some(value) = req
        .headers()
        .get("authorization")
        .and_then(|v| v.to_str().ok())
    {
        if let Some(token) = value.strip_prefix("Bearer ") {
            if !token.trim().is_empty() {
                return Some(token.trim().to_string());
            }
        }
    }
    req.cookie("privy-token")
        .map(|cookie| cookie.value().to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use actix_web::test::TestRequest;

    #[test]
    fn reads_bearer_before_cookie() {
        let req = TestRequest::default()
            .insert_header(("authorization", "Bearer header-token"))
            .cookie(actix_web::cookie::Cookie::new(
                "privy-token",
                "cookie-token",
            ))
            .to_http_request();
        assert_eq!(token_from_request(&req), Some("header-token".to_string()));
    }

    #[test]
    fn rejects_missing_configuration() {
        let req = TestRequest::default()
            .insert_header(("authorization", "Bearer token"))
            .to_http_request();
        assert!(matches!(
            AuthService::new(None, None).authenticate(&req),
            Err(AuthError::Unavailable)
        ));
    }
}
