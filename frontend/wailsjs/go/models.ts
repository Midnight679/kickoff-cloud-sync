export namespace accounts {
	
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

}

