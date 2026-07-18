#!/usr/bin/env ruby

require "json"
require "base64"
require "digest"
require "net/http"
require "open3"
require "openssl"
require "optparse"
require "securerandom"
require "timeout"
require "time"
require "uri"
require "yaml"

require_relative "database-runtime-exec"

module RenderMainnet
  PROJECT_NAME = "Robin the Claw"
  ENVIRONMENT_NAME = "Production"
  REPOSITORY_URL = "https://github.com/robintheclawdotcom/robintheclaw"
  AWS_SERVICES = %w[
    robin-lighter-provisioner
    robin-robinhood-provisioner
    robin-robinhood-signer
  ].freeze
  PUBLIC_SERVICES = %w[robintheclaw].freeze
  DATABASE_INITIALIZERS = %w[
    robin-live-control
    robin-account-publisher
    robin-lighter-provisioner
  ].freeze
  INTERNAL_SOURCE_BINDINGS = {
    "app" => {
      service: "robin-live-control",
      owner_env: "ROBIN_LIVE_CONTROL_APP_DATABASE_OWNER_URL",
      password_env: "ROBIN_APP_READONLY_DATABASE_PASSWORD",
      role: "robin_app_readonly"
    },
    "custody" => {
      service: "robin-account-publisher",
      owner_env: "ACCOUNT_PUBLISHER_CUSTODY_DATABASE_OWNER_URL",
      password_env: "ROBIN_CUSTODY_READONLY_DATABASE_PASSWORD",
      role: "robin_custody_readonly"
    },
    "execution" => {
      service: "robin-account-publisher",
      owner_env: "ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_OWNER_URL",
      password_env: "ROBIN_EXECUTION_READONLY_DATABASE_PASSWORD",
      role: "robin_execution_readonly"
    },
    "lighter" => {
      service: "robin-lighter-provisioner",
      owner_env: "LIGHTER_PROVISIONER_DATABASE_OWNER_URL",
      password_env: "ROBIN_LIGHTER_READONLY_DATABASE_PASSWORD",
      role: "robin_lighter_readonly"
    }
  }.freeze
  EVIDENCE_BASE_SERVICES = INTERNAL_SOURCE_BINDINGS.values
    .map { |binding| binding.fetch(:service) }
    .uniq
    .freeze
  CONTROLLED_SERVICES = %w[
    robin-api
    robin-research-collector
    robin-paper-agent
    robin-execution-coordinator
    robin-account-publisher
    robin-quote-authority
    robin-strategy-runner
    robin-exit-quote-publisher
    robin-live-control
    robin-sequencer-publisher-1
    robin-sequencer-publisher-2
    robin-sequencer-publisher-3
    robin-aapl-relay-1
    robin-aapl-relay-2
    robin-aapl-relay-3
    robin-lighter-provisioner
    robin-lighter-signer
    robin-robinhood-provisioner
    robin-robinhood-signer
  ].freeze
  STARTUP_BATCHES = [
    %w[
      robin-research-collector
      robin-paper-agent
      robin-sequencer-publisher-1
      robin-sequencer-publisher-2
      robin-sequencer-publisher-3
      robin-aapl-relay-1
      robin-aapl-relay-2
      robin-aapl-relay-3
      robin-lighter-provisioner
      robin-robinhood-provisioner
    ],
    %w[
      robin-lighter-signer
      robin-robinhood-signer
    ],
    %w[robin-execution-coordinator],
    %w[robin-api],
    %w[
      robin-account-publisher
      robin-quote-authority
    ],
    %w[
      robin-exit-quote-publisher
      robin-strategy-runner
    ],
    %w[
      robin-live-control
    ]
  ].freeze
  STARTUP_DEPENDENCIES = [
    %w[robin-lighter-provisioner robin-api],
    %w[robin-quote-authority robin-exit-quote-publisher],
    %w[robin-strategy-runner robin-live-control]
  ].freeze
  HEALTH_PATHS = {
    "robintheclaw" => "/"
  }.freeze
  RUNTIME_PROBE_SERVICE = "robin-live-control".freeze
  RUNTIME_INSTANCE_STABILITY_SECONDS = 15
  RUNTIME_HTTP_PROBES = {
    "robin-api" => {
      env: "ROBIN_RUNTIME_PROBE_API_HOSTPORT",
      path: "/readyz",
      status: "ready"
    },
    "robin-execution-coordinator" => {
      env: "ROBIN_RUNTIME_PROBE_COORDINATOR_HOSTPORT",
      path: "/readyz",
      status: "ready"
    },
    "robin-account-publisher" => {
      env: "ROBIN_RUNTIME_PROBE_ACCOUNT_PUBLISHER_HOSTPORT",
      path: "/readyz",
      status: "ready"
    },
    "robin-quote-authority" => {
      env: "ROBIN_RUNTIME_PROBE_QUOTE_AUTHORITY_HOSTPORT",
      path: "/health",
      status: "ready"
    },
    "robin-strategy-runner" => {
      env: "ROBIN_RUNTIME_PROBE_STRATEGY_RUNNER_HOSTPORT",
      path: "/health",
      status: "ready"
    },
    "robin-lighter-provisioner" => {
      env: "ROBIN_RUNTIME_PROBE_LIGHTER_PROVISIONER_HOSTPORT",
      path: "/readyz",
      status: "ready"
    },
    "robin-lighter-signer" => {
      env: "ROBIN_RUNTIME_PROBE_LIGHTER_SIGNER_HOSTPORT",
      path: "/readyz",
      status: "ready"
    },
    "robin-robinhood-provisioner" => {
      env: "ROBIN_RUNTIME_PROBE_ROBINHOOD_PROVISIONER_HOSTPORT",
      path: "/readyz",
      status: "ready"
    },
    "robin-robinhood-signer" => {
      env: "ROBIN_RUNTIME_PROBE_ROBINHOOD_SIGNER_HOSTPORT",
      path: "/readyz",
      status: "ready"
    }
  }.freeze
  REQUIRED_OUTPUTS = %w[
    LighterProvisionerRoleArn
    LighterEnvelopeKeyAlias
    RobinhoodKeyControlPlaneArn
    RobinhoodProvisionerRoleArn
    RobinhoodSignerRoleArn
  ].freeze
  LEGACY_ENV = {
    "robintheclaw" => %w[
      ALCHEMY_API_KEY
      ALCHEMY_POLICY_ID
      ALCHEMY_WALLET_RPC_URL
    ],
    "robin-api" => %w[
      ALCHEMY_API_KEY
      ALCHEMY_POLICY_ID
      ALCHEMY_WALLET_RPC_URL
      PERSONAL_VAULT_FACTORY
      TEST_ASSET_ADDRESS
      TEST_ASSET_DECIMALS
      TEST_ASSET_SYMBOL
      TEST_CLAIM_AMOUNT
      TEST_FAUCET_ADDRESS
    ]
  }.freeze
  STATIC_AWS_KEYS = %w[
    AWS_ACCESS_KEY_ID
    AWS_SECRET_ACCESS_KEY
    AWS_SESSION_TOKEN
    AWS_WEB_IDENTITY_TOKEN_FILE
  ].freeze
  AUTH_GROUP_KEYS = {
    "robin-lighter-provisioner-auth" => %w[LIGHTER_PROVISIONER_HMAC_KEY],
    "robin-readiness-publisher-auth" => %w[READINESS_HMAC_KEY],
    "robin-lighter-signer-auth" => %w[LIGHTER_SIGNER_HMAC_KEY],
    "robin-lighter-signer-bridge-auth" => %w[LIGHTER_SIGNER_BRIDGE_HMAC_KEY],
    "robin-lighter-publisher-bridge-auth" => %w[LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY],
    "robin-coordinator-episode-auth" => %w[COORDINATOR_EPISODE_HMAC_KEY],
    "robin-robinhood-signer-auth" => %w[ROBINHOOD_SIGNER_HMAC_KEY],
    "robin-robinhood-provisioner-auth" => %w[ROBINHOOD_PROVISIONER_HMAC_KEY],
    "robin-robinhood-signer-bridge-auth" => %w[ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY],
    "robin-coordinator-intent-auth" => %w[COORDINATOR_INTENT_HMAC_KEY],
    "robin-coordinator-exit-auth" => %w[COORDINATOR_EXIT_HMAC_KEY],
    "robin-coordinator-venue-auth" => %w[COORDINATOR_VENUE_HMAC_KEY],
    "robin-coordinator-market-auth" => %w[COORDINATOR_MARKET_HMAC_KEY],
    "robin-coordinator-account-auth" => %w[COORDINATOR_ACCOUNT_HMAC_KEY],
    "robin-coordinator-control-auth" => %w[COORDINATOR_CONTROL_HMAC_KEY],
    "robin-coordinator-registration-auth" => %w[COORDINATOR_REGISTRATION_HMAC_KEY]
  }.freeze
  CONFIG_GROUP_KEYS = {
    "robin-lighter-market-config" => %w[LIGHTER_AAPL_MARKET_INDEX],
    "robin-aapl-strategy-policy" => %w[
      AAPL_MINIMUM_NET_EDGE_PPM
      AAPL_STRATEGY_POLICY_SALT
    ],
    "robin-quote-authority-robinhood-rpc" => %w[
      ROBINHOOD_RPC_URL
      ROBINHOOD_RECONCILIATION_RPC_URL
    ],
    "robin-lighter-market-spec" => %w[
      LIGHTER_AAPL_BASE_DECIMALS
      LIGHTER_AAPL_PRICE_DECIMALS
    ],
    "robin-aapl-reference-feed-config" => %w[
      AAPL_REFERENCE_FEED
      AAPL_REFERENCE_FEED_CODE_HASH
      AAPL_SOURCE_FEED_CODE_HASH
      AAPL_SOURCE_AGGREGATOR
      AAPL_SOURCE_AGGREGATOR_CODE_HASH
      AAPL_REFERENCE_FEED_DECIMALS
      AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS
    ],
    "robin-quote-authority-public-key" => %w[
      ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY
    ],
    "robin-sequencer-feed-config" => %w[
      SEQUENCER_FEED_ADDRESS
      SEQUENCER_FEED_CODE_HASH
      SEQUENCER_USDG_PROXY_CODE_HASH
      SEQUENCER_USDG_IMPLEMENTATION_ADDRESS
      SEQUENCER_USDG_IMPLEMENTATION_CODE_HASH
      SEQUENCER_AAPL_PROXY_CODE_HASH
      SEQUENCER_AAPL_BEACON_ADDRESS
      SEQUENCER_AAPL_BEACON_CODE_HASH
      SEQUENCER_AAPL_IMPLEMENTATION_ADDRESS
      SEQUENCER_AAPL_IMPLEMENTATION_CODE_HASH
    ]
  }.freeze
  REGISTERED_ACCOUNT_RELEASE_ERROR =
    "registered-account releases require a release-only authoritative venue reconciler; " \
    "refusing to stage while five-second account snapshots would expire".freeze

  class Error < StandardError; end

  module QuiescenceReceipt
    DOMAIN = "robin.render-quiescence.v1\n"
    PAYLOAD_KEYS = %w[
      activeEpisodes
      custodyTransactionsInFlight
      evidenceSha256
      executionCommandsInFlight
      executionActionsLeased
      executionActionsPending
      expiresAt
      globalControlMode
      globalControlVersion
      inflightCommands
      lighterSigningClaims
      nonHaltedAccountControls
      nonHaltedStrategyControls
      nonFlatAccounts
      observedAt
      outboxItemsPending
      releaseCommit
      schedulerWorkInFlight
      schemaVersion
      signedUnsentTransactions
      unresolvedAmbiguities
      workspaceId
      environmentId
    ].sort.freeze
    ENVELOPE_KEYS = %w[payload signature].freeze
    SIGNATURE_KEYS = %w[algorithm keyId value].freeze
    ZERO_FIELDS = %w[
      activeEpisodes
      custodyTransactionsInFlight
      executionCommandsInFlight
      executionActionsLeased
      executionActionsPending
      inflightCommands
      lighterSigningClaims
      nonHaltedAccountControls
      nonHaltedStrategyControls
      nonFlatAccounts
      outboxItemsPending
      schedulerWorkInFlight
      signedUnsentTransactions
      unresolvedAmbiguities
    ].freeze
    MAX_AGE_SECONDS = 300

    module_function

    def sign(payload, key, now: Time.now)
      validate_payload!(payload, now: now)
      {
        "payload" => payload,
        "signature" => {
          "algorithm" => "hmac-sha256",
          "keyId" => key_id(key),
          "value" => OpenSSL::HMAC.hexdigest("SHA256", key, signed_bytes(payload))
        }
      }
    end

    def verify(envelope, key, workspace_id:, environment_id:, commit:, now: Time.now)
      exact_keys!(envelope, ENVELOPE_KEYS, "quiescence receipt")
      payload = envelope["payload"]
      signature = envelope["signature"]
      raise Error, "quiescence receipt payload must be an object" unless payload.is_a?(Hash)
      raise Error, "quiescence receipt signature must be an object" unless signature.is_a?(Hash)

      exact_keys!(signature, SIGNATURE_KEYS, "quiescence receipt signature")
      validate_payload!(
        payload,
        workspace_id: workspace_id,
        environment_id: environment_id,
        commit: commit,
        now: now
      )
      raise Error, "quiescence receipt signature algorithm is invalid" unless signature["algorithm"] == "hmac-sha256"
      raise Error, "quiescence receipt signing key does not match" unless signature["keyId"] == key_id(key)

      expected = OpenSSL::HMAC.hexdigest("SHA256", key, signed_bytes(payload))
      unless secure_compare(expected, signature["value"])
        raise Error, "quiescence receipt signature is invalid"
      end
      payload
    end

    def validate_payload!(payload, workspace_id: nil, environment_id: nil, commit: nil, now: Time.now)
      raise Error, "quiescence receipt payload must be an object" unless payload.is_a?(Hash)

      exact_keys!(payload, PAYLOAD_KEYS, "quiescence receipt payload")
      raise Error, "quiescence receipt schema version is invalid" unless payload["schemaVersion"] == 1
      unless payload["workspaceId"].to_s.match?(/\Atea-[a-z0-9]+\z/)
        raise Error, "quiescence receipt workspace is invalid"
      end
      unless payload["environmentId"].to_s.match?(/\Aevm-[a-z0-9]+\z/)
        raise Error, "quiescence receipt environment is invalid"
      end
      unless payload["releaseCommit"].to_s.match?(/\A[0-9a-f]{40}\z/)
        raise Error, "quiescence receipt release commit is invalid"
      end
      unless payload["evidenceSha256"].to_s.match?(/\A[0-9a-f]{64}\z/)
        raise Error, "quiescence receipt evidence digest is invalid"
      end
      if workspace_id && payload["workspaceId"] != workspace_id
        raise Error, "quiescence receipt workspace does not match"
      end
      if environment_id && payload["environmentId"] != environment_id
        raise Error, "quiescence receipt environment does not match"
      end
      if commit && payload["releaseCommit"] != commit
        raise Error, "quiescence receipt release commit does not match"
      end
      unless payload["globalControlMode"] == "HALTED" &&
             payload["globalControlVersion"].is_a?(Integer) &&
             payload["globalControlVersion"].positive?
        raise Error, "quiescence receipt does not prove global HALTED"
      end
      ZERO_FIELDS.each do |field|
        unless payload[field].is_a?(Integer) && payload[field].zero?
          raise Error, "quiescence receipt #{field} must be zero"
        end
      end

      observed_at = parse_time(payload["observedAt"], "observedAt")
      expires_at = parse_time(payload["expiresAt"], "expiresAt")
      if observed_at > now + 30 || observed_at < now - MAX_AGE_SECONDS
        raise Error, "quiescence receipt observation is stale or in the future"
      end
      if expires_at <= now || expires_at <= observed_at ||
         expires_at - observed_at > MAX_AGE_SECONDS
        raise Error, "quiescence receipt validity window is invalid"
      end
      payload
    end

    def read_key(path)
      stat = File.stat(path)
      raise Error, "quiescence signing key must be a regular file" unless stat.file?
      raise Error, "quiescence signing key must be mode 0600" unless (stat.mode & 0o777) == 0o600

      value = File.read(path).strip
      raise Error, "quiescence signing key must be 32-byte lowercase hex" unless value.match?(/\A[0-9a-f]{64}\z/)

      [value].pack("H*")
    rescue Errno::ENOENT, Errno::EACCES => e
      raise Error, "quiescence signing key is unavailable: #{e.message}"
    end

    def read_receipt(path)
      stat = File.stat(path)
      raise Error, "quiescence receipt must be a regular file" unless stat.file?
      raise Error, "quiescence receipt must be mode 0600" unless (stat.mode & 0o777) == 0o600

      JSON.parse(File.read(path))
    rescue JSON::ParserError
      raise Error, "quiescence receipt is invalid JSON"
    rescue Errno::ENOENT, Errno::EACCES => e
      raise Error, "quiescence receipt is unavailable: #{e.message}"
    end

    def write_receipt(path, envelope)
      write_sensitive(path, envelope, "quiescence receipt")
    end

    def write_evidence(path, evidence)
      write_sensitive(path, evidence, "quiescence evidence")
    end

    def write_sensitive(path, value, label)
      descriptor = File.open(path, File::WRONLY | File::CREAT | File::EXCL, 0o600)
      descriptor.write("#{JSON.pretty_generate(value)}\n")
      descriptor.close
    rescue Errno::EEXIST
      raise Error, "#{label} output already exists"
    rescue Errno::EACCES, Errno::ENOENT => e
      raise Error, "#{label} output is unavailable: #{e.message}"
    ensure
      descriptor&.close unless descriptor&.closed?
    end

    def signed_bytes(payload)
      DOMAIN + canonical_json(payload)
    end

    def canonical_json(value)
      case value
      when Hash
        JSON.generate(value.keys.sort.to_h { |key| [key, JSON.parse(canonical_json(value.fetch(key)))] })
      when Array
        JSON.generate(value.map { |item| JSON.parse(canonical_json(item)) })
      else
        JSON.generate(value)
      end
    end

    def key_id(key)
      Digest::SHA256.hexdigest(key)[0, 16]
    end

    def exact_keys!(value, expected, label)
      unless value.is_a?(Hash) && value.keys.sort == expected
        raise Error, "#{label} keys do not match the canonical schema"
      end
    end

    def parse_time(value, field)
      raise Error, "quiescence receipt #{field} is invalid" unless value.is_a?(String)

      parsed = Time.iso8601(value.to_s)
      unless parsed.utc_offset.zero? && value.end_with?("Z")
        raise Error, "quiescence receipt #{field} must be UTC"
      end
      parsed
    rescue ArgumentError
      raise Error, "quiescence receipt #{field} is invalid"
    end

    def secure_compare(left, right)
      return false unless right.is_a?(String) && left.bytesize == right.bytesize

      difference = 0
      left.bytes.zip(right.bytes) { |a, b| difference |= a ^ b }
      difference.zero?
    end
  end

  class PsqlQueryRunner
    def call(source:, url:, role:, sql:)
      connection = parse_url(url, role)
      environment = {
        "PGAPPNAME" => "robin-render-quiescence",
        "PGDATABASE" => connection.fetch(:database),
        "PGHOST" => connection.fetch(:host),
        "PGOPTIONS" => "-c default_transaction_read_only=on -c statement_timeout=5000 -c lock_timeout=1000",
        "PGPASSWORD" => connection.fetch(:password),
        "PGPORT" => connection.fetch(:port).to_s,
        "PGUSER" => connection.fetch(:user)
      }
      environment["PGSSLMODE"] = connection[:sslmode] if connection[:sslmode]
      command = %w[psql -X -q -A -t -v ON_ERROR_STOP=1 -c] + [sql]
      stdout, _stderr, status = Timeout.timeout(10) { Open3.capture3(environment, *command) }
      raise Error, "#{source} quiescence query failed" unless status.success?

      value = JSON.parse(stdout.strip)
      raise Error, "#{source} quiescence query returned invalid evidence" unless value.is_a?(Hash)

      value
    rescue JSON::ParserError
      raise Error, "#{source} quiescence query returned invalid JSON"
    rescue Timeout::Error
      raise Error, "#{source} quiescence query timed out"
    rescue Errno::ENOENT
      raise Error, "psql is required to collect quiescence evidence"
    end

    private

    def parse_url(value, role)
      uri = URI.parse(value.to_s)
      query = URI.decode_www_form(uri.query.to_s).to_h
      unless %w[postgres postgresql].include?(uri.scheme) &&
             uri.host && uri.path&.start_with?("/") && uri.path.length > 1 &&
             uri.fragment.nil? &&
             query.keys.all? { |key| key == "sslmode" }
        raise Error, "readonly database URL is invalid"
      end
      sslmode = query.fetch("sslmode", "require")
      unless %w[require verify-ca verify-full].include?(sslmode)
        raise Error, "readonly database URL must require TLS"
      end
      user = URI::DEFAULT_PARSER.unescape(uri.user.to_s)
      raise Error, "readonly database URL uses the wrong role" unless user == role
      password = URI::DEFAULT_PARSER.unescape(uri.password.to_s)
      raise Error, "readonly database URL has no password" if password.empty?

      {
        database: URI::DEFAULT_PARSER.unescape(uri.path.delete_prefix("/")),
        host: uri.host,
        password: password,
        port: uri.port || 5432,
        sslmode: sslmode,
        user: user
      }
    rescue URI::InvalidURIError, ArgumentError
      raise Error, "readonly database URL is invalid"
    end
  end

  class QuiescenceCollector
    ROLES = {
      "app" => "robin_app_readonly",
      "custody" => "robin_custody_readonly",
      "execution" => "robin_execution_readonly",
      "lighter" => "robin_lighter_readonly"
    }.freeze
    SOURCE_KEYS = {
      "app" => %w[commandsInFlight observedAt outboxItemsPending role transactionReadOnly],
      "custody" => %w[
        ambiguousTransactions observedAt role signedUnsentTransactions
        transactionReadOnly transactionsInFlight
      ],
      "execution" => %w[
        activeEpisodes ambiguities executionActionsLeased executionActionsPending
        executionCommandsInFlight globalControlMode globalControlVersion
        nonFlatAccounts nonHaltedAccountControls nonHaltedStrategyControls registeredAccounts
        observedAt role schedulerWorkInFlight transactionReadOnly
      ],
      "lighter" => %w[observedAt role signingClaims transactionReadOnly]
    }.transform_values(&:sort).freeze
    URL_ENV = {
      "app" => "ROBIN_APP_READONLY_DATABASE_URL",
      "custody" => "ROBIN_CUSTODY_READONLY_DATABASE_URL",
      "execution" => "ROBIN_EXECUTION_READONLY_DATABASE_URL",
      "lighter" => "ROBIN_LIGHTER_READONLY_DATABASE_URL"
    }.freeze
    QUERIES = {
      "execution" => <<~SQL,
        WITH tracked_accounts AS (
            SELECT account.execution_account_id
            FROM execution_accounts account
            JOIN execution_account_registrations registration USING (execution_account_id)
            WHERE account.status <> 'closed'
        ),
        latest_snapshots AS (
            SELECT DISTINCT ON (snapshot.execution_account_id, snapshot.source)
                   snapshot.execution_account_id, snapshot.source, snapshot.payload
            FROM execution_account_snapshots snapshot
            JOIN tracked_accounts USING (execution_account_id)
            WHERE snapshot.observed_at >= now() - interval '5 seconds'
              AND snapshot.observed_at <= now()
              AND snapshot.received_at <= now()
              AND snapshot.expires_at > now()
            ORDER BY snapshot.execution_account_id, snapshot.source,
                     snapshot.received_at DESC, snapshot.id DESC
        ),
        flat_accounts AS (
            SELECT execution_account_id
            FROM latest_snapshots
            GROUP BY execution_account_id
            HAVING count(*) FILTER (
                       WHERE source = 'lighter-auth'
                         AND payload->>'flat' = 'true'
                         AND payload->>'nonce_aligned' = 'true'
                         AND payload->>'no_unknown_orders' = 'true'
                         AND payload->>'no_unknown_positions' = 'true'
                   ) = 1
               AND count(*) FILTER (
                       WHERE source = 'robinhood-chain'
                         AND payload->>'flat' = 'true'
                         AND payload->>'wiring_verified' = 'true'
                         AND payload->>'finality_healthy' = 'true'
                   ) = 1
        )
        SELECT jsonb_build_object(
            'role', current_user,
            'transactionReadOnly', current_setting('transaction_read_only'),
            'observedAt', to_char(clock_timestamp() AT TIME ZONE 'UTC',
                                  'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
            'globalControlMode',
                (SELECT mode FROM execution_control WHERE singleton),
            'globalControlVersion',
                (SELECT version FROM execution_control WHERE singleton),
            'registeredAccounts',
                (SELECT count(*) FROM tracked_accounts),
            'nonHaltedStrategyControls',
                (SELECT count(*) FROM execution_strategy_control WHERE mode <> 'HALTED'),
            'nonHaltedAccountControls',
                (SELECT count(*)
                 FROM tracked_accounts account
                 LEFT JOIN execution_account_control control USING (execution_account_id)
                 WHERE control.mode IS DISTINCT FROM 'HALTED'),
            'activeEpisodes',
                (SELECT count(*) FROM execution_intents WHERE active),
            'nonFlatAccounts',
                (SELECT count(*)
                 FROM tracked_accounts account
                 LEFT JOIN flat_accounts USING (execution_account_id)
                 WHERE flat_accounts.execution_account_id IS NULL),
            'executionActionsPending',
                (SELECT count(*) FROM execution_actions WHERE status = 'pending'),
            'executionActionsLeased',
                (SELECT count(*) FROM execution_actions WHERE status = 'leased'),
            'executionCommandsInFlight',
                (SELECT count(*) FROM execution_account_commands
                 WHERE status IN ('processing', 'reducing', 'awaiting_owner_signature')),
            'schedulerWorkInFlight',
                (SELECT count(*) FROM live_scheduler_work
                 WHERE state IN ('pending', 'running', 'quoted', 'ambiguous')),
            'ambiguities',
                (SELECT count(*) FROM execution_actions WHERE status = 'ambiguous') +
                (SELECT count(*) FROM execution_signer_requests
                 WHERE status IN ('created', 'ambiguous')) +
                (SELECT count(*) FROM execution_incidents WHERE resolved_at IS NULL) +
                (SELECT count(*) FROM live_scheduler_work WHERE state = 'ambiguous')
        )::text;
      SQL
      "app" => <<~SQL,
        SELECT jsonb_build_object(
            'role', current_user,
            'transactionReadOnly', current_setting('transaction_read_only'),
            'observedAt', to_char(clock_timestamp() AT TIME ZONE 'UTC',
                                  'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
            'commandsInFlight',
                (SELECT count(*) FROM agent_commands
                 WHERE status IN ('pending', 'processing', 'awaiting_signature')),
            'outboxItemsPending',
                (SELECT count(*) FROM agent_command_outbox WHERE delivered_at IS NULL) +
                (SELECT count(*) FROM coordinator_account_registration_outbox
                 WHERE delivered_at IS NULL)
        )::text;
      SQL
      "custody" => <<~SQL,
        SELECT jsonb_build_object(
            'role', current_user,
            'transactionReadOnly', current_setting('transaction_read_only'),
            'observedAt', to_char(clock_timestamp() AT TIME ZONE 'UTC',
                                  'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
            'signedUnsentTransactions',
                (SELECT count(*) FROM robinhood_signer_transactions
                 WHERE status = 'signed'),
            'transactionsInFlight',
                (SELECT count(*) FROM robinhood_signer_transactions
                 WHERE status IN ('submitted', 'soft_confirmed', 'l1_posted', 'replaced')),
            'ambiguousTransactions',
                (SELECT count(*) FROM robinhood_signer_transactions
                 WHERE status IN ('ambiguous', 'quarantined'))
        )::text;
      SQL
      "lighter" => <<~SQL
        SELECT jsonb_build_object(
            'role', current_user,
            'transactionReadOnly', current_setting('transaction_read_only'),
            'observedAt', to_char(clock_timestamp() AT TIME ZONE 'UTC',
                                  'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
            'signingClaims',
                (SELECT count(*) FROM lighter_signing_requests WHERE status = 'claimed')
        )::text;
      SQL
    }.freeze

    def initialize(query_runner: PsqlQueryRunner.new, clock: -> { Time.now })
      @query_runner = query_runner
      @clock = clock
    end

    def collect_source(source:, url:)
      role = ROLES[source]
      query = QUERIES[source]
      raise Error, "unknown quiescence source" unless role && query

      value = @query_runner.call(source: source, url: url, role: role, sql: query)
      validate_source!(source, value)
      value
    end

    def collect(urls:, workspace_id:, environment_id:, commit:)
      sources = collect_sources(
        urls: urls,
        environment_id: environment_id,
        commit: commit
      )
      if sources.dig("execution", "registeredAccounts").positive?
        raise Error, REGISTERED_ACCOUNT_RELEASE_ERROR
      end
      now = @clock.call.utc
      evidence = {
        "schemaVersion" => 1,
        "observedAt" => now.iso8601,
        "sources" => sources
      }
      payload = {
        "activeEpisodes" => sources.dig("execution", "activeEpisodes"),
        "custodyTransactionsInFlight" => sources.dig("custody", "transactionsInFlight"),
        "environmentId" => environment_id,
        "evidenceSha256" => Digest::SHA256.hexdigest(QuiescenceReceipt.canonical_json(evidence)),
        "executionActionsLeased" => sources.dig("execution", "executionActionsLeased"),
        "executionActionsPending" => sources.dig("execution", "executionActionsPending"),
        "executionCommandsInFlight" => sources.dig("execution", "executionCommandsInFlight"),
        "expiresAt" => (now + QuiescenceReceipt::MAX_AGE_SECONDS).iso8601,
        "globalControlMode" => sources.dig("execution", "globalControlMode"),
        "globalControlVersion" => sources.dig("execution", "globalControlVersion"),
        "inflightCommands" => sources.dig("app", "commandsInFlight"),
        "lighterSigningClaims" => sources.dig("lighter", "signingClaims"),
        "nonFlatAccounts" => sources.dig("execution", "nonFlatAccounts"),
        "nonHaltedAccountControls" => sources.dig("execution", "nonHaltedAccountControls"),
        "nonHaltedStrategyControls" => sources.dig("execution", "nonHaltedStrategyControls"),
        "observedAt" => now.iso8601,
        "outboxItemsPending" => sources.dig("app", "outboxItemsPending"),
        "releaseCommit" => commit,
        "schedulerWorkInFlight" => sources.dig("execution", "schedulerWorkInFlight"),
        "schemaVersion" => 1,
        "signedUnsentTransactions" => sources.dig("custody", "signedUnsentTransactions"),
        "unresolvedAmbiguities" =>
          sources.dig("execution", "ambiguities") +
          sources.dig("custody", "ambiguousTransactions"),
        "workspaceId" => workspace_id
      }
      QuiescenceReceipt.validate_payload!(
        payload,
        workspace_id: workspace_id,
        environment_id: environment_id,
        commit: commit,
        now: now
      )
      [evidence, payload]
    end

    def confirm_controls_halted(urls:, environment_id:, commit:)
      execution = collect_sources(
        urls: urls,
        environment_id: environment_id,
        commit: commit
      ).fetch("execution")
      unless execution["globalControlMode"] == "HALTED" &&
             execution["globalControlVersion"].positive? &&
             execution["nonHaltedStrategyControls"].zero? &&
             execution["nonHaltedAccountControls"].zero?
        raise Error, "rollback could not confirm every execution control is HALTED"
      end
      {
        "globalControlMode" => execution.fetch("globalControlMode"),
        "globalControlVersion" => execution.fetch("globalControlVersion"),
        "observedAt" => execution.fetch("observedAt")
      }
    end

    private

    def collect_sources(urls:, environment_id:, commit:)
      requests = QUERIES.keys.sort.to_h do |source|
        url = urls[source]
        raise Error, "#{URL_ENV.fetch(source)} is required" unless url.is_a?(String) && !url.empty?

        [source, { url: url, role: ROLES.fetch(source), sql: QUERIES.fetch(source) }]
      end
      values =
        if @query_runner.respond_to?(:call_all)
          @query_runner.call_all(
            requests,
            environment_id: environment_id,
            commit: commit
          )
        else
          requests.to_h do |source, request|
            [source, @query_runner.call(source: source, **request)]
          end
        end
      unless values.is_a?(Hash) && values.keys.sort == requests.keys
        raise Error, "quiescence sources do not match the canonical schema"
      end
      sources = requests.keys.to_h do |source|
        value = values[source]
        raise Error, "#{source} quiescence evidence is missing" unless value.is_a?(Hash)

        validate_source!(source, value)
        [source, value]
      end
      sources
    end

    def validate_source!(source, value)
      unless value.keys.sort == SOURCE_KEYS.fetch(source)
        raise Error, "#{source} quiescence evidence keys do not match the canonical schema"
      end
      raise Error, "#{source} query did not use the readonly role" unless value["role"] == ROLES.fetch(source)
      raise Error, "#{source} query was not read-only" unless value["transactionReadOnly"] == "on"

      observed_at = QuiescenceReceipt.parse_time(value["observedAt"], "#{source}.observedAt")
      if (@clock.call.utc - observed_at).abs > 30
        raise Error, "#{source} database clock is outside the allowed skew"
      end
      value.each do |key, item|
        next if %w[role transactionReadOnly observedAt globalControlMode].include?(key)
        unless item.is_a?(Integer) && item >= 0
          raise Error, "#{source} quiescence evidence #{key} is invalid"
        end
      end
    end
  end

  class Manifest
    attr_reader :environment, :services, :databases, :env_groups

    def initialize(path)
      blueprint = YAML.safe_load(File.read(path), aliases: false, filename: path)
      raise Error, "Blueprint root resources are forbidden" if %w[services databases envVarGroups].any? { |key| blueprint.key?(key) }

      projects = blueprint.fetch("projects", [])
      project = unique_named(projects, PROJECT_NAME, "project")
      @environment = unique_named(project.fetch("environments", []), ENVIRONMENT_NAME, "environment")
      @services = index(@environment.fetch("services", []), "service")
      @databases = index(@environment.fetch("databases", []), "database")
      @env_groups = index(@environment.fetch("envVarGroups", []), "environment group")
    rescue Errno::ENOENT, Psych::SyntaxError, KeyError => e
      raise Error, "Blueprint is invalid: #{e.message}"
    end

    def sync_false
      services.transform_values do |service|
        service.fetch("envVars", [])
          .select { |variable| variable["sync"] == false }
          .map { |variable| variable.fetch("key") }
      end
    end

    def referenced_groups
      services.values
        .flat_map { |service| service.fetch("envVars", []).map { |variable| variable["fromGroup"] } }
        .compact
        .uniq
        .sort
    end

    private

    def unique_named(values, name, kind)
      matches = values.select { |value| value["name"] == name }
      raise Error, "Expected one #{kind} named #{name}" unless matches.length == 1

      matches.first
    end

    def index(values, kind)
      result = values.to_h { |value| [value.fetch("name"), value] }
      raise Error, "Duplicate #{kind} name" unless result.length == values.length

      result
    end
  end

  class Response
    attr_reader :code, :body

    def initialize(code:, body: "", headers: {})
      @code = code.to_i
      @body = body.to_s
      @headers = headers.transform_keys { |key| key.to_s.downcase }
    end

    def [](key)
      @headers[key.to_s.downcase]
    end
  end

  class Client
    BASE_URL = "https://api.render.com/v1"

    def self.read_token(path)
      stat = File.stat(path)
      raise Error, "Render token file must be a regular file" unless stat.file?
      raise Error, "Render token file must be mode 0600" unless (stat.mode & 0o777) == 0o600

      token = File.read(path).strip
      raise Error, "Render token file is empty" if token.empty?

      token
    rescue Errno::ENOENT, Errno::EACCES => e
      raise Error, "Render token file is unavailable: #{e.message}"
    end

    def self.retry_delay(value, now: Time.now)
      seconds = Integer(value, exception: false)
      seconds ||= [(Time.httpdate(value) - now).ceil, 1].max if value && !value.empty?
      [[seconds || 1, 1].max, 60].min
    rescue ArgumentError
      1
    end

    def initialize(token, sleeper: ->(seconds) { sleep(seconds) }, transport: nil)
      @token = token
      @sleeper = sleeper
      @transport = transport || method(:perform)
    end

    def get(path)
      request("GET", path)
    end

    def post(path, body = nil)
      request("POST", path, body)
    end

    def patch(path, body)
      request("PATCH", path, body)
    end

    def put(path, body)
      request("PUT", path, body)
    end

    def delete(path)
      request("DELETE", path)
    end

    def request(method, path, body = nil)
      attempts = 0
      loop do
        response = @transport.call(method, path, body)
        if response.code == 429 && attempts < 6
          attempts += 1
          @sleeper.call(self.class.retry_delay(response["retry-after"]))
          next
        end
        unless response.code.between?(200, 299)
          raise Error, "Render API #{method} #{path.split("?").first} failed with #{response.code}"
        end
        return nil if response.body.empty?

        return JSON.parse(response.body)
      rescue JSON::ParserError
        raise Error, "Render API #{method} #{path.split("?").first} returned invalid JSON"
      end
    end

    private

    def perform(method, path, body)
      uri = URI("#{BASE_URL}#{path}")
      request_class = {
        "GET" => Net::HTTP::Get,
        "POST" => Net::HTTP::Post,
        "PATCH" => Net::HTTP::Patch,
        "PUT" => Net::HTTP::Put,
        "DELETE" => Net::HTTP::Delete
      }.fetch(method)
      request = request_class.new(uri)
      request["Authorization"] = "Bearer #{@token}"
      request["Accept"] = "application/json"
      if body
        request["Content-Type"] = "application/json"
        request.body = JSON.generate(body)
      end
      raw = Net::HTTP.start(uri.host, uri.port, use_ssl: true, open_timeout: 10, read_timeout: 30) do |http|
        http.request(request)
      end
      Response.new(code: raw.code, body: raw.body, headers: raw.to_hash.transform_values(&:first))
    rescue SocketError, SystemCallError, Timeout::Error => e
      raise Error, "Render API #{method} #{path.split("?").first} failed: #{e.class}"
    end
  end

  class RenderJobQueryRunner
    RESULT_PREFIX = "ROBIN_RELEASE_EVIDENCE=".freeze

    def initialize(
      client:,
      workspace_id:,
      service_ids: nil,
      sleeper: ->(seconds) { sleep(seconds) }
    )
      @client = client
      @workspace_id = workspace_id
      @service_ids = service_ids
      @sleeper = sleeper
    end

    def call_all(requests, environment_id:, commit:)
      expected_sources = INTERNAL_SOURCE_BINDINGS.keys.sort
      unless requests.is_a?(Hash) && requests.keys.sort == expected_sources
        raise Error, "Render evidence jobs require every canonical source"
      end
      unless environment_id.to_s.match?(/\Aevm-[a-z0-9]+\z/) &&
             commit.to_s.match?(/\A[0-9a-f]{40}\z/)
        raise Error, "Render evidence release identity is invalid"
      end
      requests.each do |source, request|
        expected_role = INTERNAL_SOURCE_BINDINGS.fetch(source).fetch(:role)
        raise Error, "#{source} evidence job uses the wrong role" unless request.fetch(:role) == expected_role
      end

      services = service_index
      @service_ids&.each do |name, id|
        service = services[name]
        unless service &&
               service["id"] == id &&
               service["environmentId"] == environment_id &&
               service["repo"] == REPOSITORY_URL &&
               service["branch"] == "main" &&
               service["autoDeploy"] == "no"
          raise Error, "Render service #{name} does not match the prepare receipt"
        end
      end
      EVIDENCE_BASE_SERVICES.each do |name|
        service = services[name]
        raise Error, "Render service #{name} is missing" unless service
        unless service["environmentId"] == environment_id &&
               service["repo"] == REPOSITORY_URL &&
               service["branch"] == "main" &&
               service["autoDeploy"] == "no"
          raise Error, "Render evidence service #{name} has the wrong release binding"
        end
        unless service["suspended"] == "not_suspended"
          raise Error, "Render evidence service #{name} must be running before job creation"
        end
        deploys = RenderMainnet.unwrap(
          @client.get("/services/#{escape(service.fetch("id"))}/deploys?limit=1"),
          "deploy"
        )
        deploy = deploys.first
        unless deploy &&
               deploy["status"] == "live" &&
               deploy.dig("commit", "id") == commit
          raise Error, "Render evidence service #{name} is not live on #{commit}"
        end
      end

      jobs = requests.keys.to_h do |source|
        binding = INTERNAL_SOURCE_BINDINGS.fetch(source)
        service = services[binding.fetch(:service)]

        nonce = SecureRandom.hex(32)
        command = "ROBIN_RELEASE_EVIDENCE_NONCE=#{nonce} " \
                  "ruby scripts/render-mainnet-bootstrap.rb internal-source-evidence --source #{source}"
        result = @client.post(
          "/services/#{escape(service.fetch("id"))}/jobs",
          "startCommand" => command
        )
        job = result.is_a?(Hash) ? (result["job"] || result) : nil
        unless job.is_a?(Hash) && job["id"] && job["serviceId"] == service["id"]
          raise Error, "Render did not return a valid #{source} evidence job"
        end
        [source, { id: job.fetch("id"), nonce: nonce, service_id: service.fetch("id") }]
      end

      wait_for_jobs(jobs)
      jobs.to_h { |source, job| [source, read_evidence(source, job)] }
    end

    private

    def service_index
      values = RenderMainnet.unwrap(
        @client.get("/services?ownerId=#{escape(@workspace_id)}&includePreviews=false&limit=100"),
        "service"
      )
      RenderMainnet.index_named(values, "service")
    end

    def wait_for_jobs(jobs, timeout: 300)
      pending = jobs.dup
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      until pending.empty?
        pending.delete_if do |source, job|
          result = @client.get(
            "/services/#{escape(job.fetch(:service_id))}/jobs/#{escape(job.fetch(:id))}"
          )
          value = result.is_a?(Hash) ? (result["job"] || result) : nil
          unless value.is_a?(Hash) &&
                 value["id"] == job.fetch(:id) &&
                 value["serviceId"] == job.fetch(:service_id)
            raise Error, "Render returned the wrong #{source} evidence job"
          end
          status = value.fetch("status")
          raise Error, "#{source} evidence job failed with #{status}" if %w[failed canceled].include?(status)

          status == "succeeded"
        end
        break if pending.empty?
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
          raise Error, "Timed out waiting for Render evidence jobs: #{pending.keys.join(", ")}"
        end

        @sleeper.call(2)
      end
    end

    def read_evidence(source, job, timeout: 60)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      loop do
        result = @client.get(
          "/logs?ownerId=#{escape(@workspace_id)}&resource=#{escape(job.fetch(:id))}" \
          "&direction=forward&limit=100"
        )
        logs = result.fetch("logs", [])
        matches = logs.each_with_object([]) do |entry, values|
          message = entry["message"].to_s.strip
          next unless message.start_with?(RESULT_PREFIX)

          values << decode_evidence(
            message.delete_prefix(RESULT_PREFIX),
            source,
            job.fetch(:nonce)
          )
        end
        raise Error, "#{source} evidence job returned duplicate evidence" if matches.length > 1
        return matches.first if matches.length == 1
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
          raise Error, "Timed out waiting for #{source} evidence logs"
        end

        @sleeper.call(2)
      end
    end

    def decode_evidence(encoded, source, nonce)
      envelope = JSON.parse(Base64.strict_decode64(encoded))
      expected_keys = %w[evidence nonce schemaVersion source]
      unless envelope.is_a?(Hash) &&
             envelope.keys.sort == expected_keys &&
             envelope["schemaVersion"] == 1 &&
             envelope["source"] == source &&
             envelope["nonce"] == nonce &&
             envelope["evidence"].is_a?(Hash)
        raise Error, "#{source} evidence job returned the wrong envelope"
      end
      envelope.fetch("evidence")
    rescue ArgumentError, JSON::ParserError
      raise Error, "#{source} evidence job returned invalid evidence"
    end

    def escape(value)
      URI.encode_www_form_component(value)
    end
  end

  class RuntimeReadiness
    RESULT_PREFIX = "ROBIN_RUNTIME_READINESS=".freeze

    def initialize(
      client:,
      workspace_id:,
      clock: -> { Time.now },
      sleeper: ->(seconds) { sleep(seconds) }
    )
      @client = client
      @workspace_id = workspace_id
      @clock = clock
      @sleeper = sleeper
    end

    def verify(services:, names:, commit:, resumed_at:)
      unless names.is_a?(Array) && !names.empty? && names.uniq.length == names.length
        raise Error, "runtime readiness requires unique services"
      end
      unless commit.match?(/\A[0-9a-f]{40}\z/)
        raise Error, "runtime readiness commit is invalid"
      end

      first = wait_for_instances(services, names, resumed_at)
      @sleeper.call(RUNTIME_INSTANCE_STABILITY_SECONDS)
      second = instance_snapshot(services, names, resumed_at)
      unless first == second
        raise Error, "runtime instances changed during the stability window"
      end

      private_services = names.select { |name| RUNTIME_HTTP_PROBES.key?(name) }
      job_id = private_services.empty? ? nil : probe_private_services(services, private_services, commit)
      {
        "schemaVersion" => 1,
        "observedAt" => @clock.call.utc.iso8601,
        "services" => names,
        "instances" => second,
        "privateProbeJobId" => job_id
      }
    end

    private

    def wait_for_instances(services, names, resumed_at, timeout: 180)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      loop do
        snapshot = instance_snapshot(services, names, resumed_at, allow_empty: true)
        return snapshot if snapshot
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
          raise Error, "Timed out waiting for resumed runtime instances: #{names.join(", ")}"
        end

        @sleeper.call(3)
      end
    end

    def instance_snapshot(services, names, resumed_at, allow_empty: false)
      names.to_h do |name|
        service = services[name]
        raise Error, "Render service #{name} is missing" unless service

        values = @client.get("/services/#{escape(service.fetch("id"))}/instances")
        instances = RenderMainnet.unwrap(values, "instance")
        return nil if allow_empty && instances.empty?
        raise Error, "#{name} must have exactly one running instance" unless instances.length == 1

        instance = instances.first
        id = instance["id"]
        created_at = parse_instance_time(instance["createdAt"], name)
        now = @clock.call.utc
        unless id.is_a?(String) && !id.empty? &&
               created_at >= resumed_at.utc - 30 &&
               created_at <= now + 30
          raise Error, "#{name} did not create a fresh runtime instance"
        end
        [name, id]
      end
    end

    def parse_instance_time(value, name)
      raise ArgumentError unless value.is_a?(String)

      Time.iso8601(value)
    rescue ArgumentError
      raise Error, "#{name} returned an invalid runtime instance timestamp"
    end

    def probe_private_services(services, names, commit)
      probe_service = services[RUNTIME_PROBE_SERVICE]
      raise Error, "Render service #{RUNTIME_PROBE_SERVICE} is missing" unless probe_service
      service_id = probe_service.fetch("id")
      current = @client.get("/services/#{escape(service_id)}")
      unless current["id"] == service_id && current["suspended"] == "not_suspended"
        raise Error, "runtime probe base must be running before job creation"
      end
      deploys = RenderMainnet.unwrap(
        @client.get("/services/#{escape(service_id)}/deploys?limit=1"),
        "deploy"
      )
      deploy = deploys.first
      unless deploy && deploy["status"] == "live" && deploy.dig("commit", "id") == commit
        raise Error, "runtime probe base is not live on #{commit}"
      end

      nonce = SecureRandom.hex(32)
      command = "ROBIN_RUNTIME_PROBE_NONCE=#{nonce} " \
                "ruby scripts/render-mainnet-bootstrap.rb internal-runtime-probe " \
                "--commit #{commit} --services #{names.join(",")}"
      result = @client.post(
        "/services/#{escape(service_id)}/jobs",
        "startCommand" => command
      )
      job = result.is_a?(Hash) ? (result["job"] || result) : nil
      unless job.is_a?(Hash) && job["id"] && job["serviceId"] == probe_service["id"]
        raise Error, "Render did not return a valid runtime readiness job"
      end

      wait_for_job(service_id, job.fetch("id"))
      read_probe(job.fetch("id"), nonce, names, commit)
      job.fetch("id")
    end

    def wait_for_job(service_id, job_id, timeout: 180)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      loop do
        result = @client.get(
          "/services/#{escape(service_id)}/jobs/#{escape(job_id)}"
        )
        job = result.is_a?(Hash) ? (result["job"] || result) : nil
        unless job.is_a?(Hash) && job["id"] == job_id && job["serviceId"] == service_id
          raise Error, "Render returned the wrong runtime readiness job"
        end
        status = job.fetch("status")
        if %w[failed canceled cancelled].include?(status)
          raise Error, "runtime readiness job failed with #{status}"
        end
        return if status == "succeeded"
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
          raise Error, "Timed out waiting for runtime readiness job"
        end

        @sleeper.call(2)
      end
    end

    def read_probe(job_id, nonce, names, commit, timeout: 60)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      loop do
        result = @client.get(
          "/logs?ownerId=#{escape(@workspace_id)}&resource=#{escape(job_id)}" \
          "&direction=forward&limit=100"
        )
        matches = result.fetch("logs", []).each_with_object([]) do |entry, values|
          message = entry["message"].to_s.strip
          next unless message.start_with?(RESULT_PREFIX)

          values << decode_probe(message.delete_prefix(RESULT_PREFIX), nonce, names, commit)
        end
        raise Error, "runtime readiness job returned duplicate evidence" if matches.length > 1
        return if matches.length == 1
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
          raise Error, "Timed out waiting for runtime readiness logs"
        end

        @sleeper.call(2)
      end
    end

    def decode_probe(encoded, nonce, names, commit)
      envelope = JSON.parse(Base64.strict_decode64(encoded))
      expected_keys = %w[commit nonce schemaVersion services]
      unless envelope.is_a?(Hash) &&
             envelope.keys.sort == expected_keys &&
             envelope["schemaVersion"] == 1 &&
             envelope["nonce"] == nonce &&
             envelope["commit"] == commit &&
             envelope["services"] == names
        raise Error, "runtime readiness job returned the wrong envelope"
      end
      envelope
    rescue ArgumentError, JSON::ParserError
      raise Error, "runtime readiness job returned invalid evidence"
    end

    def escape(value)
      URI.encode_www_form_component(value)
    end
  end

  module_function

  def unwrap(values, key)
    raise Error, "Render API list response is invalid" unless values.is_a?(Array)

    values.map do |value|
      item = value[key] || value
      raise Error, "Render API list response is invalid" unless item.is_a?(Hash)

      item
    end
  end

  def index_named(values, kind)
    groups = values.group_by { |value| value["name"] }
    duplicate = groups.find { |_name, entries| entries.length > 1 }
    raise Error, "Duplicate Render #{kind} name: #{duplicate.first}" if duplicate

    groups.transform_values(&:first)
  end

  def parse_prepare_receipt(value)
    expected_keys = %w[
      awsServices
      environmentId
      phase
      projectId
      publicServices
      services
      workspaceId
    ]
    unless value.is_a?(Hash) && value.keys.sort == expected_keys
      raise Error, "prepare receipt keys do not match the canonical schema"
    end
    unless value["phase"] == "prepared" &&
           value["workspaceId"].to_s.match?(/\Atea-[a-z0-9]+\z/) &&
           value["projectId"].to_s.match?(/\Aprj-[a-z0-9]+\z/) &&
           value["environmentId"].to_s.match?(/\Aevm-[a-z0-9]+\z/)
      raise Error, "prepare receipt context is invalid"
    end
    services = value["services"]
    public_services = value["publicServices"]
    aws_services = value["awsServices"]
    unless services.is_a?(Hash) &&
           services.keys.sort == CONTROLLED_SERVICES.sort &&
           public_services.is_a?(Hash) &&
           public_services.keys == PUBLIC_SERVICES &&
           aws_services.is_a?(Hash) &&
           aws_services == services.slice(*AWS_SERVICES)
      raise Error, "prepare receipt services do not match the release manifest"
    end
    ids = services.values + public_services.values
    unless ids.uniq.length == ids.length &&
           ids.all? { |id| id.is_a?(String) && id.match?(/\Asrv-[a-z0-9]+\z/) }
      raise Error, "prepare receipt service IDs are invalid"
    end
    value
  end

  def parse_outputs(value)
    outputs =
      if value.is_a?(Hash) && value.dig("Stacks", 0, "Outputs")
        value.dig("Stacks", 0, "Outputs").to_h { |entry| [entry["OutputKey"], entry["OutputValue"]] }
      elsif value.is_a?(Array) && value.all? { |entry| entry.is_a?(Array) && entry.length == 2 }
        value.to_h
      elsif value.is_a?(Array)
        value.to_h { |entry| [entry["OutputKey"], entry["OutputValue"]] }
      elsif value.is_a?(Hash)
        value
      else
        raise Error, "AWS outputs are invalid"
      end
    missing = REQUIRED_OUTPUTS.reject { |key| outputs[key].is_a?(String) && !outputs[key].empty? }
    raise Error, "AWS outputs are missing: #{missing.join(", ")}" unless missing.empty?

    role_keys = %w[LighterProvisionerRoleArn RobinhoodProvisionerRoleArn RobinhoodSignerRoleArn]
    role_keys.each do |key|
      raise Error, "#{key} is invalid" unless outputs[key].match?(%r{\Aarn:aws:iam::\d{12}:role/[A-Za-z0-9+=,.@_/-]+\z})
    end
    unless outputs["RobinhoodKeyControlPlaneArn"].match?(%r{\Aarn:aws:lambda:us-east-1:\d{12}:function:[A-Za-z0-9_-]+:[1-9]\d*\z})
      raise Error, "RobinhoodKeyControlPlaneArn is invalid"
    end
    unless outputs["LighterEnvelopeKeyAlias"] == "alias/robin/lighter/credentials"
      raise Error, "LighterEnvelopeKeyAlias is invalid"
    end
    outputs
  end

  def aws_bindings(outputs)
    {
      "robin-lighter-provisioner" => {
        "AWS_ROLE_ARN" => outputs.fetch("LighterProvisionerRoleArn"),
        "AWS_KMS_KEY_ID" => outputs.fetch("LighterEnvelopeKeyAlias")
      },
      "robin-robinhood-provisioner" => {
        "AWS_ROLE_ARN" => outputs.fetch("RobinhoodProvisionerRoleArn"),
        "ROBINHOOD_KMS_PROVISION_FUNCTION_ARN" => outputs.fetch("RobinhoodKeyControlPlaneArn")
      },
      "robin-robinhood-signer" => {
        "AWS_ROLE_ARN" => outputs.fetch("RobinhoodSignerRoleArn")
      }
    }
  end

  def merge_bindings(base, additions)
    merged = base.transform_values(&:dup)
    additions.each do |service, values|
      target = merged[service] ||= {}
      values.each do |key, value|
        if target.key?(key) && target[key] != value
          raise Error, "#{service}.#{key} conflicts with the AWS stack output"
        end
        target[key] = value
      end
    end
    merged
  end

  class Bootstrap
    def initialize(
      client:,
      manifest:,
      workspace_id:,
      output: $stdout,
      clock: -> { Time.now },
      quiescence_collector: nil,
      runtime_readiness: nil,
      prepare_receipt: nil
    )
      @client = client
      @manifest = manifest
      @workspace_id = workspace_id
      @output = output
      @clock = clock
      @quiescence_collector = quiescence_collector
      @runtime_readiness = runtime_readiness
      @prepare_receipt = prepare_receipt
      @pending_deploys = {}
    end

    def inspect
      context = guard_context
      services = service_index
      summary = @manifest.services.keys.to_h do |name|
        service = services[name]
        [name, service && {
          "id" => service["id"],
          "environmentId" => service["environmentId"],
          "suspended" => service["suspended"]
        }]
      end
      emit(
        "phase" => "inspect",
        "workspaceId" => @workspace_id,
        "projectId" => context.fetch("project").fetch("id"),
        "environmentId" => context.fetch("environment").fetch("id"),
        "services" => summary
      )
    end

    def prepare(repo: nil, branch: "main", confirmation:)
      confirm!("PREPARE", confirmation)
      context = guard_context
      environment_id = context.fetch("environment").fetch("id")
      services = service_index
      existing_repo = services.fetch("robintheclaw", {})["repo"]
      if repo && existing_repo && repo != existing_repo
        raise Error, "--repo does not match the adopted web service"
      end
      repo ||= REPOSITORY_URL
      raise Error, "bootstrap repository is invalid" unless repo == REPOSITORY_URL
      raise Error, "bootstrap branch must be main" unless branch == "main"

      web = services["robintheclaw"]
      raise Error, "Render service robintheclaw is missing" unless web
      validate_bootstrap_service!("robintheclaw", web, environment_id)
      raise Error, "robintheclaw has the wrong repository binding" unless web["repo"] == repo
      raise Error, "robintheclaw must deploy from main" unless web["branch"] == branch
      CONTROLLED_SERVICES.each do |name|
        service = services[name]
        next unless service

        validate_bootstrap_service!(name, service, environment_id)
        raise Error, "#{name} has the wrong repository binding" unless service["repo"] == repo
      end

      begin
        if web["autoDeploy"] != "no"
          @client.patch("/services/#{escape(web.fetch("id"))}", "autoDeploy" => "no")
          web["autoDeploy"] = "no"
        end
        resume_services!(services, PUBLIC_SERVICES) if web["suspended"] != "not_suspended"

        CONTROLLED_SERVICES.each do |name|
          service = services[name]
          unless service
            service = create_shell(name, repo, branch, environment_id)
            services[name] = service
          end
          if service["environmentId"].nil?
            @client.post("/environments/#{escape(environment_id)}/resources", "resourceIds" => [service.fetch("id")])
          end
          @client.patch("/services/#{escape(service.fetch("id"))}", "autoDeploy" => "no") unless service["autoDeploy"] == "no"
          @client.post("/services/#{escape(service.fetch("id"))}/suspend") unless service["suspended"] == "suspended"
        end

        receipt = CONTROLLED_SERVICES.to_h do |name|
          service = wait_for_service(services.fetch(name).fetch("id"), suspended: "suspended")
          raise Error, "#{name} is not in #{ENVIRONMENT_NAME}" unless service["environmentId"] == environment_id

          [name, service.fetch("id")]
        end
        live_web = wait_for_service(web.fetch("id"), suspended: "not_suspended")
        unless live_web["environmentId"] == environment_id &&
               live_web["autoDeploy"] == "no"
          raise Error, "robintheclaw did not remain live with auto-deploy disabled"
        end
        emit(
          "phase" => "prepared",
          "workspaceId" => @workspace_id,
          "projectId" => context.fetch("project").fetch("id"),
        "environmentId" => environment_id,
        "services" => receipt,
        "publicServices" => { "robintheclaw" => web.fetch("id") },
        "awsServices" => receipt.slice(*AWS_SERVICES)
      )
      rescue StandardError => error
        rollback_services = begin
          service_index
        rescue StandardError
          services
        end
        failures = suspend_best_effort(rollback_services, CONTROLLED_SERVICES)
        suffix =
          if failures.empty?
            "all discovered controlled services are confirmed suspended"
          else
            "controlled-service suspension could not be confirmed for #{failures.join(", ")}"
          end
        raise Error, "#{error.message}; #{suffix}"
      end
    end

    def bind(outputs:, service_env:, confirmation:)
      confirm!("BIND", confirmation)
      context = guard_context
      services = service_index
      verify_prepare_receipt!(context, services)
      require_controlled_services!(services, context.fetch("environment").fetch("id"), suspended: true)
      desired = RenderMainnet.aws_bindings(outputs)
      desired = RenderMainnet.merge_bindings(desired, service_env) if service_env
      validate_bindings!(desired)

      desired.each_key do |name|
        raise Error, "Render service #{name} is missing" unless services[name]
      end
      current = desired.to_h { |name, _values| [name, service_env_map(services.fetch(name).fetch("id"))] }
      changes = desired.sum do |name, values|
        id = services.fetch(name).fetch("id")
        values.count do |key, value|
          next false if current.fetch(name)[key] == value

          @client.put("/services/#{escape(id)}/env-vars/#{escape(key)}", "value" => value)
          true
        end
      end
      desired.each do |name, values|
        actual = service_env_map(services.fetch(name).fetch("id"))
        values.each do |key, value|
          raise Error, "#{name}.#{key} did not converge" unless actual[key] == value
        end
      end
      emit("phase" => "bound", "updatedVariables" => changes, "services" => desired.keys.sort)
    end

    def initialize_databases(
      commit:,
      receipt_key:,
      evidence_output:,
      receipt_output:,
      confirmation:
    )
      confirm!("INITIALIZE-DATABASES", confirmation)
      raise Error, "commit must be a full Git SHA" unless commit.match?(/\A[0-9a-f]{40}\z/)

      context = guard_context
      environment = context.fetch("environment")
      raise Error, "#{ENVIRONMENT_NAME} must be protected" unless environment["protectedStatus"] == "protected"
      raise Error, "#{ENVIRONMENT_NAME} must enable network isolation" unless environment["networkIsolationEnabled"] == true

      services = service_index
      environment_id = environment.fetch("id")
      web_suspended = false
      begin
        require_managed_services!(services, environment_id)
        verify_prepare_receipt!(context, services)
        require_controlled_services!(services, environment_id, suspended: true)
        verify_databases!(environment_id)
        verify_generated_groups!(environment_id)
        raise Error, "internal quiescence collector is required" unless @quiescence_collector

        suspend_services!(services, PUBLIC_SERVICES)
        web_suspended = true
        deploys = {}
        DATABASE_INITIALIZERS.each do |name|
          deploys.merge!(stage_database_initializer!(services, name, commit))
        end

        refreshed = service_index
        require_managed_services!(refreshed, environment_id)
        require_controlled_services!(refreshed, environment_id, suspended: true)
        require_public_services_suspended!(refreshed)
        verify_release_commit!(refreshed, DATABASE_INITIALIZERS, commit, require_running: false)

        evidence, payload = collect_evidence!(services, environment_id, commit)
        envelope = QuiescenceReceipt.sign(payload, receipt_key, now: @clock.call)
        QuiescenceReceipt.write_evidence(evidence_output, evidence)
        QuiescenceReceipt.write_receipt(receipt_output, envelope)
        emit(
          "phase" => "databases-initialized",
          "commit" => commit,
          "deploys" => deploys,
          "evidenceSha256" => payload.fetch("evidenceSha256"),
          "keyId" => envelope.fetch("signature").fetch("keyId"),
          "services" => DATABASE_INITIALIZERS
        )
      rescue StandardError => error
        cancel_failures = cancel_pending_deploys_best_effort
        restore_failures = restore_initializer_commands_best_effort(services)
        rollback_services = CONTROLLED_SERVICES + (web_suspended ? PUBLIC_SERVICES : [])
        rollback_failures = suspend_best_effort(services, rollback_services)
        control_failures = confirm_controls_halted_best_effort(
          services,
          environment_id,
          commit
        )
        failures = (
          cancel_failures +
          restore_failures +
          rollback_failures +
          control_failures
        ).uniq.sort
        suffix =
          if failures.empty?
            "all services are confirmed suspended, reviewed commands are restored, and controls are HALTED"
          elsif failures.include?("execution-controls")
            "cleanup could not be confirmed for #{failures.join(", ")}; control state could not be confirmed HALTED"
          else
            "cleanup could not be confirmed for #{failures.join(", ")}"
          end
        raise Error, "#{error.message}; #{suffix}"
      end
    end

    def collect_receipt(
      expected_environment:,
      commit:,
      receipt_key:,
      evidence_output:,
      receipt_output:,
      confirmation:
    )
      confirm!("COLLECT-RECEIPT", confirmation)
      raise Error, "commit must be a full Git SHA" unless commit.match?(/\A[0-9a-f]{40}\z/)

      context = guard_context
      environment = context.fetch("environment")
      unless environment.fetch("id") == expected_environment &&
             environment["protectedStatus"] == "protected" &&
             environment["networkIsolationEnabled"] == true
        raise Error, "receipt collection environment does not match protected Production"
      end
      services = service_index
      environment_id = environment.fetch("id")
      require_managed_services!(services, environment_id)
      verify_prepare_receipt!(context, services)
      verify_databases!(environment_id)
      verify_generated_groups!(environment_id)
      verify_release_commit!(
        services,
        EVIDENCE_BASE_SERVICES,
        commit,
        require_running: false
      )
      evidence, payload = collect_evidence!(services, environment_id, commit)
      envelope = QuiescenceReceipt.sign(payload, receipt_key, now: @clock.call)
      QuiescenceReceipt.write_evidence(evidence_output, evidence)
      QuiescenceReceipt.write_receipt(receipt_output, envelope)
      emit(
        "phase" => "quiescence-receipt-collected",
        "keyId" => envelope.fetch("signature").fetch("keyId"),
        "evidenceSha256" => payload.fetch("evidenceSha256")
      )
    end

    def activate(
      outputs:,
      service_env:,
      env_group_config:,
      commit:,
      receipt:,
      receipt_key:,
      confirmation:
    )
      confirm!("ACTIVATE", confirmation)
      raise Error, "commit must be a full Git SHA" unless commit.match?(/\A[0-9a-f]{40}\z/)

      context = guard_context
      environment = context.fetch("environment")
      raise Error, "#{ENVIRONMENT_NAME} must be protected" unless environment["protectedStatus"] == "protected"
      raise Error, "#{ENVIRONMENT_NAME} must enable network isolation" unless environment["networkIsolationEnabled"] == true

      services = service_index
      environment_id = environment.fetch("id")
      web_suspended = false
      begin
        require_managed_services!(services, environment_id)
        verify_prepare_receipt!(context, services)
        require_controlled_services!(services, environment_id, suspended: true)
        verify_databases!(environment_id)
        verify_generated_groups!(environment_id, config_groups: env_group_config)
        verify_deployed_aws_configuration!(services)
        verify_sync_false_exact!(services, outputs, service_env)
        verify_aws_bindings!(services, outputs)
        QuiescenceReceipt.verify(
          receipt,
          receipt_key,
          workspace_id: @workspace_id,
          environment_id: environment_id,
          commit: commit,
          now: @clock.call
        )
        raise Error, "internal quiescence collector is required" unless @quiescence_collector
        raise Error, "internal runtime readiness verifier is required" unless @runtime_readiness
        verify_release_commit!(services, DATABASE_INITIALIZERS, commit, require_running: false)

        migration_services = @manifest.services
          .select { |name, service| CONTROLLED_SERVICES.include?(name) && service["preDeployCommand"] }
          .keys
        raise Error, "Blueprint has no isolated migration services" if migration_services.empty?

        deploys = {}
        quiescence = []
        quiescence << collect_quiescence!(
          services,
          receipt_key,
          environment_id,
          commit,
          "prestage"
        )
        suspend_services!(services, PUBLIC_SERVICES)
        web_suspended = true
        deletes = delete_legacy_variables!(services)
        deployment_order = STARTUP_BATCHES.flatten
        unless deployment_order.sort == CONTROLLED_SERVICES.sort &&
               deployment_order.uniq.length == deployment_order.length
          raise Error, "startup graph does not cover every controlled service exactly once"
        end
        batch_by_service = STARTUP_BATCHES.each_with_index.each_with_object({}) do |(batch, index), result|
          batch.each { |name| result[name] = index }
        end
        STARTUP_DEPENDENCIES.each do |dependency, consumer|
          unless batch_by_service.fetch(dependency) < batch_by_service.fetch(consumer)
            raise Error, "#{consumer} must start after #{dependency}"
          end
        end
        deployment_order.each do |name|
          deploys.merge!(stage_service!(services, name, commit))
        end
        require_controlled_services!(service_index, environment_id, suspended: true)
        verify_generated_groups!(environment_id, config_groups: env_group_config)
        verify_sync_false_exact!(services, outputs, service_env)
        quiescence << collect_quiescence!(
          services,
          receipt_key,
          environment_id,
          commit,
          "poststage"
        )
        verify_release_commit!(services, CONTROLLED_SERVICES, commit, require_running: false)

        runtime_readiness = []
        STARTUP_BATCHES.each_with_index do |batch, index|
          verify_generated_groups!(environment_id, config_groups: env_group_config)
          verify_sync_false_exact!(services, outputs, service_env)
          quiescence << collect_quiescence!(
            services,
            receipt_key,
            environment_id,
            commit,
            "startup-#{index + 1}"
          )
          runtime_readiness << resume_verify!(services, batch, commit)
        end

        resume_services!(services, PUBLIC_SERVICES)
        web_deploy = deploy_exact!(services, PUBLIC_SERVICES, commit)
        deploys.merge!(web_deploy)
        verify_release_commit!(services, PUBLIC_SERVICES, commit)
        emit(
          "phase" => "activated",
          "commit" => commit,
          "quiescenceEvidenceSha256" => receipt.fetch("payload").fetch("evidenceSha256"),
          "removedLegacyVariables" => deletes.map { |name, key| "#{name}.#{key}" }.sort,
          "deploys" => deploys,
          "migrationServices" => migration_services.sort,
          "quiescence" => quiescence,
          "runtimeReadiness" => runtime_readiness,
          "startupBatches" => STARTUP_BATCHES
        )
      rescue StandardError => error
        cancel_failures = cancel_pending_deploys_best_effort
        rollback_services = CONTROLLED_SERVICES + (web_suspended ? PUBLIC_SERVICES : [])
        rollback_failures = suspend_best_effort(services, rollback_services)
        control_failures = confirm_controls_halted_best_effort(
          services,
          environment_id,
          commit
        )
        rollback_failures = (
          cancel_failures +
          rollback_failures +
          control_failures
        ).uniq.sort
        message =
          if rollback_failures.empty?
            "#{error.message}; all controlled services are confirmed suspended and all controls are HALTED"
          elsif rollback_failures.include?("execution-controls")
            "#{error.message}; best-effort suspension could not be confirmed for " \
              "#{rollback_failures.join(", ")}; control state could not be confirmed HALTED"
          else
            "#{error.message}; best-effort suspension could not be confirmed for " \
              "#{rollback_failures.join(", ")}; the release remains HALTED"
          end
        raise Error, message
      end
    end

    private

    def guard_context
      owners = RenderMainnet.unwrap(@client.get("/owners?limit=100"), "owner")
      owner = owners.select { |item| item["id"] == @workspace_id }
      raise Error, "Render workspace guard failed" unless owner.length == 1

      projects = RenderMainnet.unwrap(@client.get("/projects?ownerId=#{escape(@workspace_id)}&limit=100"), "project")
      project = unique_remote(projects, PROJECT_NAME, "project")
      environments = RenderMainnet.unwrap(
        @client.get("/environments?projectId=#{escape(project.fetch("id"))}&ownerId=#{escape(@workspace_id)}&limit=100"),
        "environment"
      )
      environment = unique_remote(environments, ENVIRONMENT_NAME, "environment")
      unless environment["projectId"] == project["id"] && environment["id"].to_s.start_with?("evm-")
        raise Error, "Render environment guard failed"
      end
      { "project" => project, "environment" => environment }
    end

    def unique_remote(values, name, kind)
      matches = values.select { |item| item["name"] == name }
      raise Error, "Expected one Render #{kind} named #{name}" unless matches.length == 1

      matches.first
    end

    def verify_prepare_receipt!(context, services)
      receipt = @prepare_receipt
      raise Error, "prepare receipt is required" unless receipt
      unless receipt["workspaceId"] == @workspace_id &&
             receipt["projectId"] == context.fetch("project").fetch("id") &&
             receipt["environmentId"] == context.fetch("environment").fetch("id")
        raise Error, "prepare receipt does not match the guarded Render context"
      end
      expected = receipt.fetch("services").merge(receipt.fetch("publicServices"))
      expected.each do |name, id|
        service = services[name]
        unless service &&
               service["id"] == id &&
               service["environmentId"] == context.fetch("environment").fetch("id") &&
               service["repo"] == REPOSITORY_URL &&
               service["branch"] == "main" &&
               service["autoDeploy"] == "no"
          raise Error, "Render service #{name} does not match the prepare receipt"
        end
      end
    end

    def service_index
      values = RenderMainnet.unwrap(
        @client.get("/services?ownerId=#{escape(@workspace_id)}&includePreviews=false&limit=100"),
        "service"
      )
      RenderMainnet.index_named(values, "service")
    end

    def validate_bootstrap_service!(name, service, environment_id)
      expected = @manifest.services.fetch(name)
      expected_type = render_service_type(expected.fetch("type"))
      raise Error, "#{name} has the wrong Render type" unless service["type"] == expected_type
      runtime = service.dig("serviceDetails", "runtime") || service.dig("serviceDetails", "env")
      raise Error, "#{name} has the wrong runtime" unless runtime == expected["runtime"]
      current_environment = service["environmentId"]
      if current_environment && current_environment != environment_id
        raise Error, "#{name} belongs to a different Render environment"
      end
    end

    def create_shell(name, repo, branch, environment_id)
      expected = @manifest.services.fetch(name)
      payload = {
        "type" => render_service_type(expected.fetch("type")),
        "name" => name,
        "ownerId" => @workspace_id,
        "repo" => repo,
        "branch" => branch,
        "autoDeploy" => "no",
        "environmentId" => environment_id,
        "serviceDetails" => {
          "runtime" => expected.fetch("runtime"),
          "plan" => expected.fetch("plan"),
          "region" => expected.fetch("region"),
          "envSpecificDetails" => {
            "buildCommand" => "true",
            "startCommand" => "sleep infinity"
          }
        }
      }
      result = @client.post("/services", payload)
      service = result["service"] || result
      raise Error, "Render did not return #{name}" unless service["name"] == name && service["id"]

      service
    end

    def render_service_type(type)
      {
        "web" => "web_service",
        "pserv" => "private_service",
        "worker" => "background_worker"
      }.fetch(type)
    end

    def require_controlled_services!(services, environment_id, suspended:)
      CONTROLLED_SERVICES.each do |name|
        service = services[name]
        raise Error, "Render service #{name} is missing" unless service
        validate_bootstrap_service!(name, service, environment_id)
        if suspended && service["suspended"] != "suspended"
          raise Error, "#{name} must be suspended"
        end
      end
    end

    def require_aws_services!(services, environment_id, suspended:)
      AWS_SERVICES.each do |name|
        service = services[name]
        raise Error, "Render service #{name} is missing" unless service
        validate_bootstrap_service!(name, service, environment_id)
        if suspended && service["suspended"] != "suspended"
          raise Error, "#{name} must be suspended"
        end
      end
    end

    def require_managed_services!(services, environment_id)
      @manifest.services.each do |name, expected|
        service = services[name]
        raise Error, "Render service #{name} is missing" unless service
        raise Error, "#{name} is outside #{ENVIRONMENT_NAME}" unless service["environmentId"] == environment_id
        expected_type = render_service_type(expected.fetch("type"))
        raise Error, "#{name} has the wrong Render type" unless service["type"] == expected_type
        runtime = service.dig("serviceDetails", "runtime") || service.dig("serviceDetails", "env")
        raise Error, "#{name} has the wrong runtime" unless runtime == expected["runtime"]
        raise Error, "#{name} must run in virginia" unless service.dig("serviceDetails", "region") == "virginia"
        raise Error, "#{name} has the wrong plan" unless service.dig("serviceDetails", "plan") == expected["plan"]
        raise Error, "#{name} must deploy from main" unless service["branch"] == "main"
        raise Error, "#{name} has the wrong repository binding" unless service["repo"] == REPOSITORY_URL
        unless service["autoDeploy"] == "no"
          raise Error, "#{name} has the wrong auto-deploy policy"
        end
        details = service.dig("serviceDetails", "envSpecificDetails") || {}
        raise Error, "#{name} has the wrong build command" unless details["buildCommand"] == expected["buildCommand"]
        raise Error, "#{name} has the wrong start command" unless details["startCommand"] == expected["startCommand"]
        expected_health = HEALTH_PATHS[name]
        if expected_health && service.dig("serviceDetails", "healthCheckPath") != expected_health
          raise Error, "#{name} has the wrong health check path"
        end
      end
    end

    def verify_databases!(environment_id)
      databases = RenderMainnet.index_named(
        RenderMainnet.unwrap(@client.get("/postgres?ownerId=#{escape(@workspace_id)}&limit=100"), "postgres"),
        "database"
      )
      @manifest.databases.each do |name, expected|
        database = databases[name]
        raise Error, "Render database #{name} is missing" unless database
        raise Error, "#{name} is outside #{ENVIRONMENT_NAME}" unless database["environmentId"] == environment_id
        raise Error, "#{name} is not available" unless database["status"] == "available"
        raise Error, "#{name} has the wrong database name" unless database["databaseName"] == expected.fetch("databaseName")
        raise Error, "#{name} has the wrong owner role" unless database["databaseUser"] == expected.fetch("user")
        raise Error, "#{name} must run in virginia" unless database["region"] == "virginia"
        raise Error, "#{name} has the wrong plan" unless database["plan"] == expected.fetch("plan").tr("-", "_")
        raise Error, "#{name} has the wrong storage size" unless database["diskSizeGB"] == expected.fetch("diskSizeGB")
        unless database["diskAutoscalingEnabled"] == expected.fetch("storageAutoscalingEnabled")
          raise Error, "#{name} has the wrong storage autoscaling state"
        end
        expected_pool = expected.fetch("connectionPool", "none")
        raise Error, "#{name} has the wrong connection pool" unless database["connectionPool"] == expected_pool
        verify_external_database_block!(name, database)
        unless database["highAvailabilityEnabled"] == expected.dig("highAvailability", "enabled")
          raise Error, "#{name} has the wrong HA state"
        end
        raise Error, "#{name} has the wrong PostgreSQL version" unless database["version"] == expected.fetch("postgresMajorVersion")
      end
    end

    def verify_external_database_block!(name, database)
      allowlist = database["ipAllowList"]
      return if allowlist == []
      raise Error, "#{name} must block external database access" unless allowlist.nil?

      info = @client.get("/postgres/#{escape(database.fetch("id"))}/connection-info")
      raise Error, "#{name} external isolation could not be verified" unless info.is_a?(Hash)

      url = info.fetch("externalConnectionString", "")
      uri = URI.parse(url)
      unless %w[postgres postgresql].include?(uri.scheme) &&
             uri.host && uri.user && uri.password && uri.path&.length.to_i > 1
        raise Error, "#{name} external isolation could not be verified"
      end
      environment = {
        "PGDATABASE" => URI::DEFAULT_PARSER.unescape(uri.path.delete_prefix("/")),
        "PGHOST" => uri.host,
        "PGPASSWORD" => URI::DEFAULT_PARSER.unescape(uri.password),
        "PGPORT" => (uri.port || 5432).to_s,
        "PGSSLMODE" => "require",
        "PGUSER" => URI::DEFAULT_PARSER.unescape(uri.user),
        "PGCONNECT_TIMEOUT" => "5"
      }
      2.times do
        _stdout, stderr, status = Timeout.timeout(10) do
          Open3.capture3(
            environment,
            "psql",
            "-X",
            "-Atq",
            "-v",
            "ON_ERROR_STOP=1",
            "-c",
            "SELECT 1"
          )
        end
        raise Error, "#{name} permits external database access" if status.success?
        unless stderr.include?("SSL connection has been closed unexpectedly")
          raise Error, "#{name} external isolation could not be verified"
        end
      end
    rescue KeyError, URI::InvalidURIError, Timeout::Error, Errno::ENOENT
      raise Error, "#{name} external isolation could not be verified"
    end

    def verify_generated_groups!(environment_id, config_groups: nil)
      groups = RenderMainnet.index_named(
        RenderMainnet.unwrap(@client.get("/env-groups?ownerId=#{escape(@workspace_id)}&limit=100"), "envGroup"),
        "environment group"
      )
      @manifest.referenced_groups.each do |name|
        group = groups[name]
        raise Error, "Render environment group #{name} is missing" unless group
        details = @client.get("/env-groups/#{escape(group.fetch("id"))}")
        variables = details.fetch("envVars", [])
        if variables.empty? || variables.any? { |variable| !variable["value"].is_a?(String) || variable["value"].empty? }
          raise Error, "#{name} contains an empty variable"
        end

        if @manifest.env_groups.key?(name)
          raise Error, "#{name} is outside #{ENVIRONMENT_NAME}" unless group["environmentId"] == environment_id
          expected = @manifest.env_groups.fetch(name).fetch("envVars").map { |variable| variable.fetch("key") }
          unless variables.map { |variable| variable["key"] }.sort == expected.sort
            raise Error, "#{name} variables do not match the Blueprint"
          end
          variables.each do |variable|
            decoded = Base64.strict_decode64(variable["value"].to_s)
            raise Error, "#{name}.#{variable["key"]} is not 32 bytes" unless decoded.bytesize == 32
          rescue ArgumentError
            raise Error, "#{name}.#{variable["key"]} is not valid base64"
          end
        elsif AUTH_GROUP_KEYS.key?(name)
          expected = AUTH_GROUP_KEYS.fetch(name)
          unless variables.map { |variable| variable["key"] }.sort == expected.sort
            raise Error, "#{name} variables do not match the authentication manifest"
          end
          variables.each do |variable|
            unless variable["value"].match?(/\A[0-9a-f]{64}\z/)
              raise Error, "#{name}.#{variable["key"]} is not 32-byte lowercase hex"
            end
          end
        elsif CONFIG_GROUP_KEYS.key?(name)
          expected = CONFIG_GROUP_KEYS.fetch(name)
          unless variables.map { |variable| variable["key"] }.sort == expected.sort
            raise Error, "#{name} variables do not match the reviewed config manifest"
          end
          if config_groups
            reviewed = config_groups.fetch(name)
            variables.each do |variable|
              unless variable["value"] == reviewed.fetch(variable.fetch("key"))
                raise Error, "#{name}.#{variable["key"]} does not match reviewed configuration"
              end
            end
          end
        else
          raise Error, "#{name} has no environment group policy"
        end
      end
    end

    def deploy_exact!(services, names, commit)
      return {} if names.empty?

      deploys = names.to_h do |name|
        id = services.fetch(name).fetch("id")
        result = @client.post("/services/#{escape(id)}/deploys", "commitId" => commit)
        deploy = result.is_a?(Hash) ? (result["deploy"] || result) : nil
        raise Error, "Render did not return a deploy for #{name}" unless deploy.is_a?(Hash) && deploy["id"]

        @pending_deploys[name] = {
          service_id: id,
          deploy_id: deploy.fetch("id")
        }
        [name, deploy.fetch("id")]
      end
      wait_for_deploys(services, deploys)
      deploys.each_key { |name| @pending_deploys.delete(name) }
      deploys
    end

    def stage_service!(services, name, commit)
      set_service_commands!(services, name, start_command: "sleep infinity")
      resume_services!(services, [name])
      deploys = deploy_exact!(services, [name], commit)
      verify_release_commit!(services, [name], commit)
      deploys
    ensure
      suspend_services!(services, [name])
      restore_reviewed_command!(services, name)
    end

    def stage_database_initializer!(services, name, commit)
      stage_service!(services, name, commit)
    end

    def set_service_commands!(services, name, start_command:)
      expected = @manifest.services.fetch(name)
      id = services.fetch(name).fetch("id")
      details = {
        "envSpecificDetails" => { "startCommand" => start_command }
      }
      details["preDeployCommand"] = expected["preDeployCommand"] if expected["preDeployCommand"]
      @client.patch("/services/#{escape(id)}", "serviceDetails" => details)
      wait_for_start_command(id, start_command)
    end

    def restore_reviewed_command!(services, name)
      expected = @manifest.services.fetch(name)
      set_service_commands!(
        services,
        name,
        start_command: expected.fetch("startCommand")
      )
    end

    def restore_initializer_commands_best_effort(services)
      DATABASE_INITIALIZERS.each_with_object([]) do |name, failures|
        next unless services[name]

        begin
          restore_reviewed_command!(services, name)
        rescue StandardError
          failures << name
        end
      end
    end

    def require_public_services_suspended!(services)
      PUBLIC_SERVICES.each do |name|
        service = services[name]
        raise Error, "Render service #{name} is missing" unless service
        raise Error, "#{name} must be suspended" unless service["suspended"] == "suspended"
      end
    end

    def job_quiescence_urls
      QuiescenceCollector::URL_ENV.keys.to_h { |source| [source, "render-job://#{source}"] }
    end

    def resume_verify!(services, names, commit)
      resumed_at = @clock.call.utc
      resume_services!(services, names)
      verify_release_commit!(services, names, commit)
      verify = lambda do
        @runtime_readiness.verify(
          services: services,
          names: names,
          commit: commit,
          resumed_at: resumed_at
        )
      end
      evidence =
        if names.any? { |name| RUNTIME_HTTP_PROBES.key?(name) }
          with_noop_services!(services, [RUNTIME_PROBE_SERVICE]) do
            verify_release_commit!(
              services,
              [RUNTIME_PROBE_SERVICE],
              commit,
              require_running: false
            )
            verify.call
          end
        else
          verify.call
        end
      verify_release_commit!(services, names, commit)
      evidence
    end

    def resume_services!(services, names)
      names.each do |name|
        id = services.fetch(name).fetch("id")
        @client.post("/services/#{escape(id)}/resume")
      end
      names.each do |name|
        wait_for_service(services.fetch(name).fetch("id"), suspended: "not_suspended")
      end
    end

    def suspend_services!(services, names)
      names.uniq.each do |name|
        id = services.fetch(name).fetch("id")
        current = @client.get("/services/#{escape(id)}")
        @client.post("/services/#{escape(id)}/suspend") unless current["suspended"] == "suspended"
      end
      names.uniq.each do |name|
        wait_for_service(services.fetch(name).fetch("id"), suspended: "suspended")
      end
    end

    def verify_release_commit!(services, names, commit, require_running: true)
      names.each do |name|
        id = services.fetch(name).fetch("id")
        deploys = RenderMainnet.unwrap(
          @client.get("/services/#{escape(id)}/deploys?limit=1"),
          "deploy"
        )
        deploy = deploys.first
        unless deploy && deploy["status"] == "live" && deploy.dig("commit", "id") == commit
          raise Error, "#{name} is not live on #{commit}"
        end
        expected_health = HEALTH_PATHS[name]
        next unless expected_health

        service = @client.get("/services/#{escape(id)}")
        unless (!require_running || service["suspended"] == "not_suspended") &&
               service.dig("serviceDetails", "healthCheckPath") == expected_health
          raise Error, "#{name} did not pass its reviewed health check"
        end
      end
    end

    def collect_quiescence!(services, key, environment_id, commit, phase)
      evidence, payload = collect_evidence!(services, environment_id, commit)
      envelope = QuiescenceReceipt.sign(payload, key, now: @clock.call)
      QuiescenceReceipt.verify(
        envelope,
        key,
        workspace_id: @workspace_id,
        environment_id: environment_id,
        commit: commit,
        now: @clock.call
      )
      {
        "phase" => phase,
        "evidenceSha256" => payload.fetch("evidenceSha256"),
        "keyId" => envelope.fetch("signature").fetch("keyId"),
        "sourceObservedAt" => evidence.fetch("sources").transform_values { |source| source.fetch("observedAt") }
      }
    end

    def collect_evidence!(services, environment_id, commit)
      with_evidence_bases!(services) do
        @quiescence_collector.collect(
          urls: job_quiescence_urls,
          workspace_id: @workspace_id,
          environment_id: environment_id,
          commit: commit
        )
      end
    end

    def confirm_controls_halted_best_effort(services, environment_id, commit)
      with_evidence_bases!(services) do
        @quiescence_collector.confirm_controls_halted(
          urls: job_quiescence_urls,
          environment_id: environment_id,
          commit: commit
        )
      end
      []
    rescue StandardError
      ["execution-controls"]
    end

    def with_evidence_bases!(services)
      with_noop_services!(services, EVIDENCE_BASE_SERVICES) { yield }
    end

    def with_noop_services!(services, names)
      temporary = names.uniq.select do |name|
        service = services.fetch(name)
        state = @client.get("/services/#{escape(service.fetch("id"))}")
        unless %w[suspended not_suspended].include?(state["suspended"])
          raise Error, "Render evidence service #{name} returned an invalid suspension state"
        end
        state["suspended"] == "suspended"
      end

      operation_error = nil
      result = nil
      cleanup_failures = []
      begin
        temporary.each do |name|
          set_service_commands!(services, name, start_command: "sleep infinity")
        end
        resume_services!(services, temporary)
        result = yield
      rescue StandardError => error
        operation_error = error
      ensure
        cleanup_failures.concat(suspend_best_effort(services, temporary))
        temporary.each do |name|
          begin
            restore_reviewed_command!(services, name)
          rescue StandardError
            cleanup_failures << name
          end
        end
      end

      unless cleanup_failures.empty?
        message = "evidence-base cleanup could not be confirmed for #{cleanup_failures.uniq.sort.join(", ")}"
        message = "#{operation_error.message}; #{message}" if operation_error
        raise Error, message
      end
      raise operation_error if operation_error

      result
    end

    def delete_legacy_variables!(services)
      direct_env = LEGACY_ENV.keys.to_h do |name|
        [name, service_env_map(services.fetch(name).fetch("id"))]
      end
      deletes = LEGACY_ENV.flat_map do |name, keys|
        keys.each_with_object([]) do |key, result|
          result << [name, key] if direct_env.fetch(name).key?(key)
        end
      end
      deletes.each do |name, key|
        id = services.fetch(name).fetch("id")
        @client.delete("/services/#{escape(id)}/env-vars/#{escape(key)}")
      end
      deletes.each do |name, key|
        actual = service_env_map(services.fetch(name).fetch("id"))
        raise Error, "#{name}.#{key} was not removed" if actual.key?(key)
      end
      deletes
    end

    def suspend_best_effort(services, names)
      failures = []
      names.each do |name|
        service = services[name]
        next unless service

        begin
          current = @client.get("/services/#{escape(service.fetch("id"))}")
          unless current["suspended"] == "suspended"
            @client.post("/services/#{escape(service.fetch("id"))}/suspend")
          end
        rescue StandardError
          failures << name
        end
      end
      names.each do |name|
        service = services[name]
        next unless service

        begin
          wait_for_service(service.fetch("id"), suspended: "suspended", timeout: 60)
        rescue StandardError
          failures << name
        end
      end
      failures.uniq.sort
    end

    def cancel_pending_deploys_best_effort
      terminal = %w[live deactivated build_failed update_failed pre_deploy_failed canceled]
      failures = []
      @pending_deploys.each do |name, pending|
        begin
          path = "/services/#{escape(pending.fetch(:service_id))}/deploys/#{escape(pending.fetch(:deploy_id))}"
          deploy = @client.get(path)
          unless terminal.include?(deploy["status"])
            @client.post("#{path}/cancel")
            deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + 60
            loop do
              deploy = @client.get(path)
              break if terminal.include?(deploy["status"])
              raise Error, "deploy cancellation timed out" if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline

              sleep 2
            end
          end
        rescue StandardError
          failures << name
        end
      end
      @pending_deploys.clear
      failures
    end

    def verify_deployed_aws_configuration!(services)
      AWS_SERVICES.each do |name|
        expected = @manifest.services.fetch(name)
        service = @client.get("/services/#{escape(services.fetch(name).fetch("id"))}")
        details = service.dig("serviceDetails", "envSpecificDetails") || {}
        raise Error, "#{name} still has the bootstrap build command" unless details["buildCommand"] == expected["buildCommand"]
        raise Error, "#{name} still has the bootstrap start command" unless details["startCommand"] == expected["startCommand"]
        raise Error, "#{name} must deploy from main" unless service["branch"] == "main"
        raise Error, "#{name} auto-deploy must remain disabled" unless service["autoDeploy"] == "no"
      end
    end

    def verify_sync_false!(services)
      @manifest.sync_false.each do |name, keys|
        next if keys.empty?

        actual = service_env_map(services.fetch(name).fetch("id"))
        missing = keys.reject { |key| actual[key].is_a?(String) && !actual[key].empty? }
        raise Error, "#{name} is missing required direct variables: #{missing.join(", ")}" unless missing.empty?
      end
    end

    def verify_sync_false_exact!(services, outputs, service_env)
      desired = RenderMainnet.merge_bindings(RenderMainnet.aws_bindings(outputs), service_env)
      validate_bindings!(desired)
      @manifest.sync_false.each do |name, keys|
        next if keys.empty?

        expected = desired.fetch(name) do
          raise Error, "#{name} has no reviewed direct environment configuration"
        end
        missing = keys - expected.keys
        extra = expected.keys - keys
        unless missing.empty? && extra.empty?
          raise Error, "#{name} direct environment keys do not match the reviewed manifest"
        end
        actual = service_env_map(services.fetch(name).fetch("id"))
        keys.each do |key|
          unless actual[key] == expected.fetch(key)
            raise Error, "#{name}.#{key} does not match reviewed configuration"
          end
        end
        if AWS_SERVICES.include?(name)
          forbidden = STATIC_AWS_KEYS.select { |key| actual.key?(key) }
          unless forbidden.empty?
            raise Error, "#{name} contains forbidden AWS credential variables: #{forbidden.join(", ")}"
          end
        end
      end
    end

    def verify_aws_bindings!(services, outputs)
      RenderMainnet.aws_bindings(outputs).each do |name, expected|
        actual = service_env_map(services.fetch(name).fetch("id"))
        expected.each do |key, value|
          raise Error, "#{name}.#{key} does not match the AWS stack output" unless actual[key] == value
        end
        forbidden = STATIC_AWS_KEYS.select { |key| actual.key?(key) }
        raise Error, "#{name} contains forbidden AWS credential variables: #{forbidden.join(", ")}" unless forbidden.empty?
      end
    end

    def validate_bindings!(desired)
      allowed = @manifest.sync_false
      desired.each do |name, values|
        raise Error, "Unknown Render service in binding file: #{name}" unless allowed.key?(name)
        raise Error, "#{name} bindings must be an object" unless values.is_a?(Hash)
        values.each do |key, value|
          raise Error, "#{name}.#{key} is not declared sync:false" unless allowed.fetch(name).include?(key)
          raise Error, "#{name}.#{key} must be non-empty" unless value.is_a?(String) && !value.empty?
        end
      end
    end

    def service_env_map(service_id)
      values = RenderMainnet.unwrap(@client.get("/services/#{escape(service_id)}/env-vars?limit=100"), "envVar")
      keys = values.map { |value| value["key"] }
      raise Error, "Duplicate direct environment variable" unless keys.uniq.length == keys.length

      values.to_h { |value| [value.fetch("key"), value["value"]] }
    end

    def wait_for_service(id, suspended:, timeout: 180)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      loop do
        service = @client.get("/services/#{escape(id)}")
        return service if service["suspended"] == suspended
        raise Error, "Timed out waiting for Render service #{id}" if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline

        sleep 3
      end
    end

    def wait_for_start_command(id, expected, timeout: 180)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      loop do
        service = @client.get("/services/#{escape(id)}")
        actual = service.dig("serviceDetails", "envSpecificDetails", "startCommand")
        return service if actual == expected
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
          raise Error, "Timed out waiting for Render service #{id} command update"
        end

        sleep 3
      end
    end

    def wait_for_deploys(services, deploys, timeout: 10_200)
      pending = deploys.dup
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      until pending.empty?
        pending.delete_if do |name, deploy_id|
          service_id = services.fetch(name).fetch("id")
          deploy = @client.get("/services/#{escape(service_id)}/deploys/#{escape(deploy_id)}")
          status = deploy.fetch("status")
          if %w[build_failed update_failed pre_deploy_failed canceled deactivated].include?(status)
            raise Error, "#{name} deploy failed with #{status}"
          end

          status == "live"
        end
        break if pending.empty?
        raise Error, "Timed out waiting for Render deploys: #{pending.keys.join(", ")}" if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline

        sleep 10
      end
    end

    def confirm!(expected, actual)
      raise Error, "Phase requires --confirm #{expected}" unless actual == expected
    end

    def emit(value)
      @output.puts(JSON.pretty_generate(value))
    end

    def escape(value)
      URI.encode_www_form_component(value)
    end
  end

  def read_json(path, secret: false)
    if secret
      stat = File.stat(path)
      raise Error, "#{File.basename(path)} must be a regular file" unless stat.file?
      raise Error, "#{File.basename(path)} must be mode 0600" unless (stat.mode & 0o777) == 0o600
    end
    JSON.parse(File.read(path))
  rescue JSON::ParserError
    raise Error, "Unable to parse #{File.basename(path)}"
  rescue Errno::ENOENT, Errno::EACCES => e
    raise Error, "Unable to read #{File.basename(path)}: #{e.message}"
  end

  def parse_service_env(value)
    services = value["services"]
    raise Error, "service environment file must contain a services object" unless services.is_a?(Hash)

    services.each do |name, variables|
      raise Error, "#{name} variables must be an object" unless variables.is_a?(Hash)
      variables.each do |key, variable|
        unless key.is_a?(String) && variable.is_a?(String) && !variable.empty?
          raise Error, "#{name}.#{key} must be non-empty"
        end
      end
    end
    services
  end

  def parse_env_group_config(value)
    unless value.is_a?(Hash) && value.keys == ["groups"] && value["groups"].is_a?(Hash)
      raise Error, "environment group config must contain only a groups object"
    end
    groups = value.fetch("groups")
    unless groups.keys.sort == CONFIG_GROUP_KEYS.keys.sort
      raise Error, "environment group config names do not match the reviewed manifest"
    end
    CONFIG_GROUP_KEYS.each do |name, expected_keys|
      variables = groups[name]
      unless variables.is_a?(Hash) && variables.keys.sort == expected_keys.sort
        raise Error, "#{name} keys do not match the reviewed manifest"
      end
      variables.each do |key, variable|
        unless variable.is_a?(String) && !variable.empty?
          raise Error, "#{name}.#{key} must be non-empty"
        end
      end
    end
    groups
  end

  def quiescence_urls(source = ENV)
    QuiescenceCollector::URL_ENV.to_h do |name, variable|
      value = source[variable]
      raise Error, "#{variable} is required" unless value.is_a?(String) && !value.empty?

      [name, value]
    end
  end

  def emit_internal_source_evidence(source, environment = ENV, output = $stdout)
    binding = INTERNAL_SOURCE_BINDINGS[source]
    raise Error, "unknown internal quiescence source" unless binding

    nonce = environment["ROBIN_RELEASE_EVIDENCE_NONCE"].to_s
    unless nonce.match?(/\A[0-9a-f]{64}\z/)
      raise Error, "internal quiescence nonce is invalid"
    end
    owner_url = environment[binding.fetch(:owner_env)]
    password = environment[binding.fetch(:password_env)]
    if owner_url.to_s.empty? || password.to_s.empty?
      raise Error, "#{source} internal database binding is unavailable"
    end
    url = DatabaseRuntime.runtime_url(owner_url, binding.fetch(:role), password)
    evidence = QuiescenceCollector.new.collect_source(source: source, url: url)
    envelope = {
      "schemaVersion" => 1,
      "nonce" => nonce,
      "source" => source,
      "evidence" => evidence
    }
    output.puts("#{RenderJobQueryRunner::RESULT_PREFIX}#{Base64.strict_encode64(JSON.generate(envelope))}")
  rescue DatabaseRuntime::Error => e
    raise Error, e.message
  end

  def emit_internal_runtime_probe(
    commit:,
    services:,
    environment: ENV,
    output: $stdout,
    sleeper: ->(seconds) { sleep(seconds) },
    requester: nil,
    timeout: 120
  )
    unless commit.match?(/\A[0-9a-f]{40}\z/) &&
           environment["RENDER"] == "true" &&
           environment["RENDER_GIT_COMMIT"] == commit
      raise Error, "internal runtime probe release identity is invalid"
    end
    unless services.is_a?(Array) && !services.empty? &&
           services.uniq.length == services.length &&
           services.all? { |name| RUNTIME_HTTP_PROBES.key?(name) }
      raise Error, "internal runtime probe services are invalid"
    end
    nonce = environment["ROBIN_RUNTIME_PROBE_NONCE"].to_s
    unless nonce.match?(/\A[0-9a-f]{64}\z/)
      raise Error, "internal runtime probe nonce is invalid"
    end

    targets = services.to_h do |name|
      probe = RUNTIME_HTTP_PROBES.fetch(name)
      hostport = environment[probe.fetch(:env)].to_s
      unless hostport.match?(/\A[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?:[0-9]{2,5}\z/)
        raise Error, "#{name} internal runtime endpoint is invalid"
      end
      [name, URI("http://#{hostport}#{probe.fetch(:path)}")]
    end

    deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
    loop do
      ready = services.all? do |name|
        response = request_runtime_probe(targets.fetch(name), requester)
        next false unless response.code.to_i == 200
        next false if response.body.to_s.bytesize > 16_384

        body = JSON.parse(response.body.to_s)
        body.is_a?(Hash) && body["status"] == RUNTIME_HTTP_PROBES.fetch(name).fetch(:status)
      rescue JSON::ParserError, SocketError, SystemCallError, Timeout::Error, EOFError
        false
      end
      break if ready
      if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
        raise Error, "internal runtime endpoints did not become ready: #{services.join(", ")}"
      end

      sleeper.call(2)
    end

    envelope = {
      "schemaVersion" => 1,
      "nonce" => nonce,
      "commit" => commit,
      "services" => services
    }
    output.puts("#{RuntimeReadiness::RESULT_PREFIX}#{Base64.strict_encode64(JSON.generate(envelope))}")
  end

  def request_runtime_probe(uri, requester)
    return requester.call(uri) if requester

    http = Net::HTTP.new(uri.host, uri.port)
    http.open_timeout = 2
    http.read_timeout = 2
    http.write_timeout = 2 if http.respond_to?(:write_timeout=)
    http.start do
      http.request(Net::HTTP::Get.new(uri.request_uri))
    end
  end

  def run(argv)
    phase = argv.shift
    options = {
      blueprint: File.expand_path("../render.yaml", __dir__),
      branch: "main"
    }
    parser = OptionParser.new do |flags|
      flags.banner = "usage: render-mainnet-bootstrap.rb " \
                     "inspect|prepare|bind|initialize-databases|collect-receipt|activate [options]"
      flags.on("--workspace ID") { |value| options[:workspace] = value }
      flags.on("--environment ID") { |value| options[:environment] = value }
      flags.on("--token-file PATH") { |value| options[:token_file] = value }
      flags.on("--blueprint PATH") { |value| options[:blueprint] = value }
      flags.on("--repo URL") { |value| options[:repo] = value }
      flags.on("--branch NAME") { |value| options[:branch] = value }
      flags.on("--aws-outputs PATH") { |value| options[:aws_outputs] = value }
      flags.on("--service-env PATH") { |value| options[:service_env] = value }
      flags.on("--env-group-config PATH") { |value| options[:env_group_config] = value }
      flags.on("--commit SHA") { |value| options[:commit] = value }
      flags.on("--quiescence-receipt PATH") { |value| options[:quiescence_receipt] = value }
      flags.on("--quiescence-key-file PATH") { |value| options[:quiescence_key_file] = value }
      flags.on("--prepare-receipt PATH") { |value| options[:prepare_receipt] = value }
      flags.on("--evidence-output PATH") { |value| options[:evidence_output] = value }
      flags.on("--output PATH") { |value| options[:output] = value }
      flags.on("--source NAME") { |value| options[:source] = value }
      flags.on("--services NAMES") { |value| options[:services] = value }
      flags.on("--confirm PHASE") { |value| options[:confirmation] = value }
    end
    parser.parse!(argv)
    phases = %w[
      inspect prepare bind initialize-databases collect-receipt activate
      internal-source-evidence internal-runtime-probe
    ]
    raise Error, parser.banner unless phases.include?(phase)

    if phase == "internal-source-evidence"
      raise Error, "--source is required" unless options[:source]

      emit_internal_source_evidence(options.fetch(:source))
      return
    end

    if phase == "internal-runtime-probe"
      raise Error, "--commit is required" unless options[:commit]
      raise Error, "--services is required" unless options[:services]

      emit_internal_runtime_probe(
        commit: options.fetch(:commit),
        services: options.fetch(:services).split(",")
      )
      return
    end

    raise Error, "--workspace is required" unless options[:workspace]
    raise Error, "--token-file is required" unless options[:token_file]

    token = Client.read_token(options.fetch(:token_file))
    client = Client.new(token)
    prepare_receipt =
      if %w[bind initialize-databases collect-receipt activate].include?(phase)
        raise Error, "--prepare-receipt is required" unless options[:prepare_receipt]

        parse_prepare_receipt(
          read_json(options.fetch(:prepare_receipt), secret: true)
        )
      end
    service_ids = prepare_receipt &&
                  prepare_receipt.fetch("services").merge(
                    prepare_receipt.fetch("publicServices")
                  )
    bootstrap = Bootstrap.new(
      client: client,
      manifest: Manifest.new(options.fetch(:blueprint)),
      workspace_id: options.fetch(:workspace),
      quiescence_collector: QuiescenceCollector.new(
        query_runner: RenderJobQueryRunner.new(
          client: client,
          workspace_id: options.fetch(:workspace),
          service_ids: service_ids
        )
      ),
      runtime_readiness: RuntimeReadiness.new(
        client: client,
        workspace_id: options.fetch(:workspace)
      ),
      prepare_receipt: prepare_receipt
    )
    case phase
    when "inspect"
      bootstrap.inspect
    when "prepare"
      bootstrap.prepare(
        repo: options[:repo],
        branch: options.fetch(:branch),
        confirmation: options[:confirmation]
      )
    when "bind"
      raise Error, "--aws-outputs is required" unless options[:aws_outputs]
      outputs = parse_outputs(read_json(options.fetch(:aws_outputs)))
      service_env = options[:service_env] && parse_service_env(read_json(options.fetch(:service_env), secret: true))
      bootstrap.bind(outputs: outputs, service_env: service_env, confirmation: options[:confirmation])
    when "initialize-databases"
      raise Error, "--commit is required" unless options[:commit]
      raise Error, "--quiescence-key-file is required" unless options[:quiescence_key_file]
      raise Error, "--evidence-output is required" unless options[:evidence_output]
      raise Error, "--output is required" unless options[:output]
      receipt_key = QuiescenceReceipt.read_key(options.fetch(:quiescence_key_file))
      bootstrap.initialize_databases(
        commit: options.fetch(:commit),
        receipt_key: receipt_key,
        evidence_output: options.fetch(:evidence_output),
        receipt_output: options.fetch(:output),
        confirmation: options[:confirmation]
      )
    when "collect-receipt"
      raise Error, "--environment is required" unless options[:environment]
      raise Error, "--commit is required" unless options[:commit]
      raise Error, "--quiescence-key-file is required" unless options[:quiescence_key_file]
      raise Error, "--evidence-output is required" unless options[:evidence_output]
      raise Error, "--output is required" unless options[:output]
      bootstrap.collect_receipt(
        expected_environment: options.fetch(:environment),
        commit: options.fetch(:commit),
        receipt_key: QuiescenceReceipt.read_key(options.fetch(:quiescence_key_file)),
        evidence_output: options.fetch(:evidence_output),
        receipt_output: options.fetch(:output),
        confirmation: options[:confirmation]
      )
    when "activate"
      raise Error, "--aws-outputs is required" unless options[:aws_outputs]
      raise Error, "--service-env is required" unless options[:service_env]
      raise Error, "--env-group-config is required" unless options[:env_group_config]
      raise Error, "--commit is required" unless options[:commit]
      raise Error, "--quiescence-receipt is required" unless options[:quiescence_receipt]
      raise Error, "--quiescence-key-file is required" unless options[:quiescence_key_file]
      outputs = parse_outputs(read_json(options.fetch(:aws_outputs)))
      service_env = parse_service_env(read_json(options.fetch(:service_env), secret: true))
      env_group_config = parse_env_group_config(
        read_json(options.fetch(:env_group_config), secret: true)
      )
      receipt = QuiescenceReceipt.read_receipt(options.fetch(:quiescence_receipt))
      receipt_key = QuiescenceReceipt.read_key(options.fetch(:quiescence_key_file))
      bootstrap.activate(
        outputs: outputs,
        service_env: service_env,
        env_group_config: env_group_config,
        commit: options.fetch(:commit),
        receipt: receipt,
        receipt_key: receipt_key,
        confirmation: options[:confirmation]
      )
    end
  end
end

if $PROGRAM_NAME == __FILE__
  begin
    RenderMainnet.run(ARGV)
  rescue OptionParser::ParseError, RenderMainnet::Error => e
    warn(e.message)
    exit 1
  end
end
