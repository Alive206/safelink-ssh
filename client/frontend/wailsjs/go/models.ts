export namespace config {

	export class SSHCfg {
	    addr: string;
	    user: string;
	    identity_file: string;
	    passphrase: string;
	    password: string;

	    static createFrom(source: any = {}) {
	        return new SSHCfg(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addr = source["addr"];
	        this.user = source["user"];
	        this.identity_file = source["identity_file"];
	        this.passphrase = source["passphrase"];
	        this.password = source["password"];
	    }
	}
	export class TunCfg {
	    subnet: string;
	    dns: string[];
	    auto_route: boolean;
	    tls_cert: string;
	    tls_key: string;
	    sni: string;
	    pin_sha256: string;
	    padding?: boolean;

	    static createFrom(source: any = {}) {
	        return new TunCfg(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.subnet = source["subnet"];
	        this.dns = source["dns"];
	        this.auto_route = source["auto_route"];
	        this.tls_cert = source["tls_cert"];
	        this.tls_key = source["tls_key"];
	        this.sni = source["sni"];
	        this.pin_sha256 = source["pin_sha256"];
	        this.padding = source["padding"];
	    }
	}
	export class TunnelCfg {
	    name: string;
	    mode: string;
	    ssh: SSHCfg;
	    listen: string;
	    forward: string;
	    transport: string;
	    tun: TunCfg;

	    static createFrom(source: any = {}) {
	        return new TunnelCfg(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.mode = source["mode"];
	        this.ssh = this.convertValues(source["ssh"], SSHCfg);
	        this.listen = source["listen"];
	        this.forward = source["forward"];
	        this.transport = source["transport"];
	        this.tun = this.convertValues(source["tun"], TunCfg);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {

	export class LogEntry {
	    time: string;
	    level: string;
	    module: string;
	    message: string;

	    static createFrom(source: any = {}) {
	        return new LogEntry(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.module = source["module"];
	        this.message = source["message"];
	    }
	}

}

export namespace manager {

	export class Status {
	    config: config.TunnelCfg;
	    state: string;
	    last_error?: string;
	    // Go type: time
	    started_at?: any;
	    uptime_seconds: number;
	    run_count: number;
	    stats: tunnel.Snapshot;
	    route_active?: boolean;

	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = this.convertValues(source["config"], config.TunnelCfg);
	        this.state = source["state"];
	        this.last_error = source["last_error"];
	        this.started_at = this.convertValues(source["started_at"], null);
	        this.uptime_seconds = source["uptime_seconds"];
	        this.run_count = source["run_count"];
	        this.stats = this.convertValues(source["stats"], tunnel.Snapshot);
	        this.route_active = source["route_active"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace proxycore {

	export class Status {
	    state: string;
	    node_name?: string;
	    socks_addr: string;
	    http_addr: string;
	    mode: string;
	    core_path?: string;
	    core_available: boolean;
	    last_error?: string;
	    started_at?: string;
	    upload_speed_bps: number;
	    download_speed_bps: number;
	    upload_total_bytes: number;
	    download_total_bytes: number;

	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.node_name = source["node_name"];
	        this.socks_addr = source["socks_addr"];
	        this.http_addr = source["http_addr"];
	        this.mode = source["mode"];
	        this.core_path = source["core_path"];
	        this.core_available = source["core_available"];
	        this.last_error = source["last_error"];
	        this.started_at = source["started_at"];
	        this.upload_speed_bps = source["upload_speed_bps"];
	        this.download_speed_bps = source["download_speed_bps"];
	        this.upload_total_bytes = source["upload_total_bytes"];
	        this.download_total_bytes = source["download_total_bytes"];
	    }
	}
	export class TestResult {
	    node_name: string;
	    ok: boolean;
	    latency_ms?: number;
	    speed_mbps?: number;
	    error?: string;
	    tested_at: string;

	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.node_name = source["node_name"];
	        this.ok = source["ok"];
	        this.latency_ms = source["latency_ms"];
	        this.speed_mbps = source["speed_mbps"];
	        this.error = source["error"];
	        this.tested_at = source["tested_at"];
	    }
	}

}

export namespace proxysubscription {

	export class TransportOptions {
	    type?: string;
	    path?: string;
	    host?: string;
	    headers?: {[key: string]: string};

	    static createFrom(source: any = {}) {
	        return new TransportOptions(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.path = source["path"];
	        this.host = source["host"];
	        this.headers = source["headers"];
	    }
	}
	export class TLSOptions {
	    enabled: boolean;
	    server_name?: string;
	    insecure?: boolean;
	    alpn?: string[];
	    public_key?: string;
	    short_id?: string;
	    fingerprint?: string;

	    static createFrom(source: any = {}) {
	        return new TLSOptions(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.server_name = source["server_name"];
	        this.insecure = source["insecure"];
	        this.alpn = source["alpn"];
	        this.public_key = source["public_key"];
	        this.short_id = source["short_id"];
	        this.fingerprint = source["fingerprint"];
	    }
	}
	export class ProxyNode {
	    id?: string;
	    subscription_id?: string;
	    name: string;
	    protocol: string;
	    server: string;
	    port: number;
	    method?: string;
	    password?: string;
	    uuid?: string;
	    alter_id?: number;
	    security?: string;
	    flow?: string;
	    udp?: boolean;
	    tls?: TLSOptions;
	    transport?: TransportOptions;

	    static createFrom(source: any = {}) {
	        return new ProxyNode(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.subscription_id = source["subscription_id"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.server = source["server"];
	        this.port = source["port"];
	        this.method = source["method"];
	        this.password = source["password"];
	        this.uuid = source["uuid"];
	        this.alter_id = source["alter_id"];
	        this.security = source["security"];
	        this.flow = source["flow"];
	        this.udp = source["udp"];
	        this.tls = this.convertValues(source["tls"], TLSOptions);
	        this.transport = this.convertValues(source["transport"], TransportOptions);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}


}

export namespace sshsession {

	export class Config {
	    addr: string;
	    user: string;
	    identity_file: string;
	    passphrase: string;
	    password: string;
	    rows: number;
	    cols: number;

	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addr = source["addr"];
	        this.user = source["user"];
	        this.identity_file = source["identity_file"];
	        this.passphrase = source["passphrase"];
	        this.password = source["password"];
	        this.rows = source["rows"];
	        this.cols = source["cols"];
	    }
	}

}

export namespace store {

	export class ClientSettings {
	    proxy_mode: string;
	    system_proxy: boolean;
	    auto_start: boolean;
	    bypass_lan: boolean;
	    auto_connect: boolean;
	    minimize_to_tray: boolean;

	    static createFrom(source: any = {}) {
	        return new ClientSettings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proxy_mode = source["proxy_mode"];
	        this.system_proxy = source["system_proxy"];
	        this.auto_start = source["auto_start"];
	        this.bypass_lan = source["bypass_lan"];
	        this.auto_connect = source["auto_connect"];
	        this.minimize_to_tray = source["minimize_to_tray"];
	    }
	}
	export class SSHConnection {
	    id: string;
	    name: string;
	    addr: string;
	    user: string;
	    password: string;

	    static createFrom(source: any = {}) {
	        return new SSHConnection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.addr = source["addr"];
	        this.user = source["user"];
	        this.password = source["password"];
	    }
	}
	export class SubscriptionSource {
	    id: string;
	    name: string;
	    url: string;
	    format: string;
	    kind?: string;
	    enabled: boolean;
	    auto_refresh: boolean;
	    interval_min: number;
	    last_refresh?: string;
	    last_error?: string;
	    tunnel_count: number;
	    node_count?: number;

	    static createFrom(source: any = {}) {
	        return new SubscriptionSource(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.url = source["url"];
	        this.format = source["format"];
	        this.kind = source["kind"];
	        this.enabled = source["enabled"];
	        this.auto_refresh = source["auto_refresh"];
	        this.interval_min = source["interval_min"];
	        this.last_refresh = source["last_refresh"];
	        this.last_error = source["last_error"];
	        this.tunnel_count = source["tunnel_count"];
	        this.node_count = source["node_count"];
	    }
	}

}

export namespace tunnel {

	export class DriverStatus {
	    os: string;
	    installed: boolean;
	    driver_path?: string;
	    message: string;
	    can_auto_fix: boolean;
	    is_admin: boolean;
	    can_request_admin: boolean;

	    static createFrom(source: any = {}) {
	        return new DriverStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.os = source["os"];
	        this.installed = source["installed"];
	        this.driver_path = source["driver_path"];
	        this.message = source["message"];
	        this.can_auto_fix = source["can_auto_fix"];
	        this.is_admin = source["is_admin"];
	        this.can_request_admin = source["can_request_admin"];
	    }
	}
	export class Snapshot {
	    bytes_in: number;
	    bytes_out: number;
	    conn_active: number;
	    conn_total: number;

	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bytes_in = source["bytes_in"];
	        this.bytes_out = source["bytes_out"];
	        this.conn_active = source["conn_active"];
	        this.conn_total = source["conn_total"];
	    }
	}

}
