#!/usr/bin/env ruby

require "cgi"
require "uri"

url = ENV.delete("ROBIN_DATABASE_URL")
abort("ROBIN_DATABASE_URL is required") if url.to_s.empty?

begin
  uri = URI.parse(url)
  user = URI.decode_www_form_component(uri.user.to_s)
  password = URI.decode_www_form_component(uri.password.to_s)
  database = URI.decode_www_form_component(uri.path.to_s.delete_prefix("/"))
  query = CGI.parse(uri.query.to_s)
rescue URI::InvalidURIError, URI::InvalidComponentError, ArgumentError
  abort("ROBIN_DATABASE_URL is invalid")
end

unless %w[postgres postgresql].include?(uri.scheme) &&
       uri.host && !user.empty? && !password.empty? && !database.empty? &&
       uri.fragment.nil?
  abort("ROBIN_DATABASE_URL is invalid")
end

allowed = %w[sslmode connect_timeout application_name]
unknown = query.keys - allowed
abort("ROBIN_DATABASE_URL contains unsupported options") unless unknown.empty?
abort("ROBIN_DATABASE_URL contains duplicate options") if query.values.any? { |values| values.length != 1 }

environment = ENV.to_h.merge(
  "PGHOST" => uri.host,
  "PGPORT" => (uri.port || 5432).to_s,
  "PGUSER" => user,
  "PGPASSWORD" => password,
  "PGDATABASE" => database
)
environment["PGSSLMODE"] = query.fetch("sslmode").first if query.key?("sslmode")
environment["PGCONNECT_TIMEOUT"] = query.fetch("connect_timeout").first if query.key?("connect_timeout")
environment["PGAPPNAME"] = query.fetch("application_name").first if query.key?("application_name")

exec(environment, "psql", *ARGV, unsetenv_others: true)
