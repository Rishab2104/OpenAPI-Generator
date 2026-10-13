from flask import Flask

from .config import API_PREFIX
from .orders import OrderAPI, orders_bp
from .users import bp as users_bp


def create_app():
    app = Flask(__name__)
    app.register_blueprint(users_bp, url_prefix=f"{API_PREFIX}/users")
    app.register_blueprint(orders_bp)
    app.add_url_rule(API_PREFIX + "/orders/<int:order_id>", view_func=OrderAPI.as_view("order"))

    @app.route("/health")
    def health():
        return {"ok": True}

    @app.get("/version")
    def version():
        return {"version": "1.0"}

    return app
