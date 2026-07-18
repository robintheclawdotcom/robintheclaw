#!/usr/bin/env ruby

require "optparse"
require "uri"

module DatabaseRuntime
  class Error < StandardError; end

  Binding = Struct.new(:owner_env, :role, :password_env, :target_env, keyword_init: true)
  POSTGRES_ENV = %w[
    PGAPPNAME PGCHANNELBINDING PGCONNECT_TIMEOUT PGDATABASE PGHOST PGHOSTADDR
    PGPASSFILE PGPASSWORD PGPORT PGSERVICE PGSERVICEFILE PGSSLCERT PGSSLCRL
    PGSSLKEY PGSSLMODE PGSSLROOTCERT PGTARGETSESSIONATTRS PGUSER
  ].freeze

  module_function

  def parse_binding(value)
    fields = value.split(",", -1)
    raise Error, "database binding must contain four comma-separated fields" unless fields.length == 4

    owner_env, role, password_env, target_env = fields
    unless [owner_env, password_env, target_env].all? { |item| item.match?(/\A[A-Z][A-Z0-9_]*\z/) }
      raise Error, "database binding contains an invalid environment variable name"
    end
    unless [owner_env, password_env, target_env].uniq.length == 3
      raise Error, "database binding environment variables must be distinct"
    end
    raise Error, "database binding contains an invalid role" unless role.match?(/\Arobin_[a-z0-9_]+\z/)

    Binding.new(
      owner_env: owner_env,
      role: role,
      password_env: password_env,
      target_env: target_env
    )
  end

  def runtime_url(owner_url, role, password)
    raise Error, "database password must contain at least 32 characters" if password.to_s.length < 32

    uri = URI.parse(owner_url)
    unless %w[postgres postgresql].include?(uri.scheme) && uri.host && uri.path && uri.path != "/"
      raise Error, "database owner URL is invalid"
    end
    raise Error, "database owner URL must include an owner identity" if uri.user.to_s.empty?
    raise Error, "database owner and runtime roles must differ" if URI.decode_www_form_component(uri.user) == role

    encoded_password = URI.encode_www_form_component(password).gsub("+", "%20")
    uri.userinfo = "#{role}:#{encoded_password}"
    uri.to_s
  rescue URI::InvalidURIError, URI::InvalidComponentError
    raise Error, "database owner URL is invalid"
  end

  def build_environment(bindings, source, removals = [])
    targets = bindings.map(&:target_env)
    raise Error, "database binding targets must be unique" unless targets.uniq.length == targets.length

    source_variables = bindings.flat_map { |binding| [binding.owner_env, binding.password_env] }
    unless (targets & source_variables).empty? && (targets & removals).empty?
      raise Error, "database runtime target cannot be removed"
    end

    result = source.to_h.reject do |name, _value|
      POSTGRES_ENV.include?(name) ||
        name == "DATABASE_URL" ||
        name.end_with?("_DATABASE_URL", "_DATABASE_OWNER_URL", "_DATABASE_PASSWORD")
    end
    bindings.each do |binding|
      owner_url = source.fetch(binding.owner_env) do
        raise Error, "#{binding.owner_env} is required"
      end
      password = source.fetch(binding.password_env) do
        raise Error, "#{binding.password_env} is required"
      end
      result[binding.target_env] = runtime_url(owner_url, binding.role, password)
    end
    bindings.each do |binding|
      result.delete(binding.owner_env)
      result.delete(binding.password_env)
    end
    removals.each { |name| result.delete(name) }
    result
  end

  def run(argv, source: ENV)
    values = []
    removals = []
    parser = OptionParser.new do |options|
      options.on("--binding VALUE") { |value| values << parse_binding(value) }
      options.on("--remove-env NAME") do |name|
        raise Error, "invalid removal environment variable name" unless name.match?(/\A[A-Z][A-Z0-9_]*\z/)

        removals << name
      end
    end
    command = parser.order(argv)
    raise Error, "at least one database binding is required" if values.empty?
    raise Error, "runtime command is required" if command.empty?

    environment = build_environment(values, source, removals)
    exec(environment, *command, unsetenv_others: true)
  end
end

if $PROGRAM_NAME == __FILE__
  DatabaseRuntime.run(ARGV)
end
