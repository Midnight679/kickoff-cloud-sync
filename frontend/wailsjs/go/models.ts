export namespace accounts {
	
	export class PollResult {
	    found: number;
	    uploaded: number;
	    failed: number;
	
	    static createFrom(source: any = {}) {
	        return new PollResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.found = source["found"];
	        this.uploaded = source["uploaded"];
	        this.failed = source["failed"];
	    }
	}
	export class AccountView {
	    id: string;
	    display_name: string;
	    friendly_name?: string;
	    auth_status: string;
	    paused: boolean;
	    // Go type: time
	    last_poll_time?: any;
	    // Go type: time
	    next_poll_time?: any;
	    has_token: boolean;
	    replay_visibility: string;
	    last_poll?: PollResult;
	
	    static createFrom(source: any = {}) {
	        return new AccountView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.display_name = source["display_name"];
	        this.friendly_name = source["friendly_name"];
	        this.auth_status = source["auth_status"];
	        this.paused = source["paused"];
	        this.last_poll_time = this.convertValues(source["last_poll_time"], null);
	        this.next_poll_time = this.convertValues(source["next_poll_time"], null);
	        this.has_token = source["has_token"];
	        this.replay_visibility = source["replay_visibility"];
	        this.last_poll = this.convertValues(source["last_poll"], PollResult);
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
	export class PendingAccountView {
	    pending_id: string;
	    epic_display_name: string;
	
	    static createFrom(source: any = {}) {
	        return new PendingAccountView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pending_id = source["pending_id"];
	        this.epic_display_name = source["epic_display_name"];
	    }
	}
	
	export class RetryFailedUploadsResult {
	    attempted: number;
	    succeeded: number;
	
	    static createFrom(source: any = {}) {
	        return new RetryFailedUploadsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.attempted = source["attempted"];
	        this.succeeded = source["succeeded"];
	    }
	}

}

export namespace updatecheck {
	
	export class Info {
	    available: boolean;
	    current_version: string;
	    latest_version?: string;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.current_version = source["current_version"];
	        this.latest_version = source["latest_version"];
	        this.url = source["url"];
	    }
	}

}

