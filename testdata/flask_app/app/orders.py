from flask import Blueprint
from flask.views import MethodView

orders_bp = Blueprint("orders", __name__, url_prefix="/api/orders")
reports_bp = Blueprint("reports", __name__, url_prefix="/reports")
orders_bp.register_blueprint(reports_bp)


@orders_bp.post("/")
def create_order():
    return {}, 201


@reports_bp.route("/<any(daily, weekly):period>")
def report(period):
    return {}


class OrderAPI(MethodView):
    def get(self, order_id):
        return {}

    def patch(self, order_id):
        return {}

    def _helper(self):
        pass
