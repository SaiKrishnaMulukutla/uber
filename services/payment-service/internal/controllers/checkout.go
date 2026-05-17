package controllers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/jwt"
)

// CheckoutWS upgrades to WebSocket and waits for payment completion signal.
// Requires a valid access or checkout JWT passed as ?token=<jwt> — browsers
// cannot send custom headers during a WebSocket upgrade.
func (h *Handler) CheckoutWS(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("token")
	if raw == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	claims, err := jwt.Validate(raw)
	if err != nil || (claims.TokenType != "access" && claims.TokenType != "checkout") {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	paymentID := chi.URLParam(r, "id")
	h.hub.HandleWS(w, r, paymentID)
}

// Checkout serves the payment page for a given payment ID.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "id")
	token := r.URL.Query().Get("token")

	payment, err := h.svc.GetByPaymentID(r.Context(), paymentID)
	if err != nil || payment == nil {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}

	if payment.Status == "COMPLETED" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, renderSuccess(payment.Amount, payment.PaymentMethod, payment.TripID))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderCheckout(
		paymentID, token, payment.Amount, payment.TripID,
		payment.ProviderOrderID, h.keyID, payment.PaymentMethod,
	))
}

func renderCheckout(paymentID, token string, amount float64, tripID, providerOrderID, keyID, paymentMethod string) string {
	wsPath := "/payments/ws/" + paymentID
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>RideGo — Payment</title>
<script src="https://checkout.razorpay.com/v1/checkout.js"></script>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{background:linear-gradient(135deg,#f0f0f0 0%%,#e4e4e4 100%%);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:16px}
.checkout-card{background:#fff;border-radius:20px;box-shadow:0 20px 60px rgba(0,0,0,.15),0 4px 16px rgba(0,0,0,.08);width:100%%;max-width:420px;overflow:hidden}
.header{background:linear-gradient(135deg,#0f0f0f 0%%,#2d2d2d 60%%,#1a1a1a 100%%);padding:28px 24px 22px;position:relative;overflow:hidden}
.header::before{content:'';position:absolute;top:-40px;right:-40px;width:130px;height:130px;background:rgba(255,255,255,.04);border-radius:50%%}
.header::after{content:'';position:absolute;bottom:-20px;left:10px;width:80px;height:80px;background:rgba(255,255,255,.03);border-radius:50%%}
.header-row{display:flex;align-items:flex-start;justify-content:space-between;position:relative;z-index:1}
.brand-name{color:#fff;font-size:22px;font-weight:800;letter-spacing:-.5px}
.brand-sub{color:rgba(255,255,255,.45);font-size:11px;margin-top:3px;letter-spacing:.5px;text-transform:uppercase}
.fare-block{text-align:right}
.fare-amount{color:#fff;font-size:26px;font-weight:800;letter-spacing:-.5px}
.fare-label{color:rgba(255,255,255,.45);font-size:11px;margin-top:3px;letter-spacing:.5px;text-transform:uppercase}
.progress-bar{display:flex;align-items:center;gap:5px;margin-top:18px;position:relative;z-index:1}
.dot{width:6px;height:6px;border-radius:50%%;background:rgba(255,255,255,.25)}
.dot.done{background:#fff}
.dot.active{background:#fff;width:20px;border-radius:3px}
.progress-label{color:rgba(255,255,255,.5);font-size:11px;margin-left:4px}
.tabs-wrapper{padding:14px 16px 0;background:#fff}
.tabs{display:flex;background:#f3f3f3;border-radius:12px;padding:4px;gap:2px}
.tab{flex:1;padding:10px 4px;text-align:center;font-size:13px;font-weight:600;cursor:pointer;color:#888;border-radius:9px;transition:all .25s cubic-bezier(.4,0,.2,1);display:flex;align-items:center;justify-content:center;gap:5px;white-space:nowrap;user-select:none}
.tab.active{background:#fff;color:#000;box-shadow:0 2px 8px rgba(0,0,0,.12)}
.tab-icon{font-size:14px}
.body{padding:20px 24px 28px}
.panel{display:none;animation:fadeIn .2s ease}
.panel.active{display:block}
@keyframes fadeIn{from{opacity:0;transform:translateY(4px)}to{opacity:1;transform:translateY(0)}}
.method-title{font-size:16px;font-weight:700;margin-bottom:3px}
.method-sub{font-size:13px;color:#888;margin-bottom:18px}
.steps{list-style:none;margin-bottom:20px}
.step{display:flex;align-items:flex-start;gap:14px;padding:11px 0;border-bottom:1px solid #f0f0f0}
.step:last-child{border:none}
.step-num{width:28px;height:28px;border-radius:50%%;background:#000;color:#fff;display:flex;align-items:center;justify-content:center;font-size:12px;font-weight:700;flex-shrink:0}
.step-text{font-size:13px;color:#444;line-height:1.5;padding-top:5px}
.step-text strong{color:#000}
.waiting-box{background:#f9f9f9;border-radius:12px;padding:20px;text-align:center;border:1.5px dashed #e0e0e0}
.pulse-ring{width:44px;height:44px;border:2.5px solid #000;border-radius:50%%;margin:0 auto 12px;animation:pulse 1.4s ease-in-out infinite}
@keyframes pulse{0%%,100%%{opacity:1;transform:scale(1)}50%%{opacity:.25;transform:scale(1.1)}}
.waiting-title{font-size:14px;font-weight:700;margin-bottom:4px}
.waiting-sub{font-size:12px;color:#888}
.upi-apps{display:flex;gap:10px;justify-content:center;margin-bottom:20px}
.upi-app{display:flex;flex-direction:column;align-items:center;gap:6px;font-size:10px;font-weight:600;color:#555}
.upi-app-icon{width:48px;height:48px;border-radius:14px;display:flex;align-items:center;justify-content:center;box-shadow:0 2px 8px rgba(0,0,0,.1)}
.gpay{background:#fff;border:1.5px solid #e8e8e8;font-size:9px;font-weight:900;color:#4285f4;letter-spacing:-.3px}
.phonepe{background:linear-gradient(135deg,#6739b7,#4b2891);color:#fff;font-size:16px}
.paytm{background:linear-gradient(135deg,#00b9f1,#0078c8);color:#fff;font-size:10px;font-weight:900}
.bhim{background:linear-gradient(135deg,#1a73e8,#0d47a1);color:#fff;font-size:12px;font-weight:900}
.test-hint{background:#f0f7ff;border:1px solid #c8e0ff;border-radius:8px;padding:10px 14px;margin-bottom:20px;display:flex;align-items:center;gap:10px}
.hint-label{color:#1a73e8;font-weight:700;font-size:12px;white-space:nowrap}
.hint-value{color:#333;font-family:monospace;font-size:13px;font-weight:600}
.card-logos{display:flex;gap:6px;margin-bottom:14px;flex-wrap:wrap}
.card-logo{background:#f5f5f5;border-radius:5px;padding:5px 10px;font-size:11px;font-weight:800;color:#444;letter-spacing:.5px}
.test-card-box{background:linear-gradient(135deg,#f8f8f8,#f0f0f0);border-radius:12px;padding:14px 16px;margin-bottom:18px;border:1px solid #e8e8e8}
.test-card-label{font-size:11px;color:#888;font-weight:600;text-transform:uppercase;letter-spacing:.5px;margin-bottom:6px}
.test-card-num{font-family:monospace;font-size:19px;font-weight:700;color:#000;letter-spacing:2px;margin-bottom:4px}
.test-card-sub{font-size:11px;color:#888;margin-bottom:8px}
.copy-btn{display:inline-flex;align-items:center;gap:4px;background:#000;color:#fff;border:none;border-radius:6px;padding:5px 12px;font-size:11px;font-weight:600;cursor:pointer;transition:background .2s}
.copy-btn:hover{background:#333}
.btn{width:100%%;padding:16px;background:#000;color:#fff;border:none;border-radius:12px;font-size:16px;font-weight:700;cursor:pointer;transition:all .2s;display:flex;align-items:center;justify-content:center;gap:8px;letter-spacing:-.2px}
.btn:hover{background:#222;transform:translateY(-1px);box-shadow:0 4px 12px rgba(0,0,0,.2)}
.btn:active{transform:translateY(0);box-shadow:none}
.btn:disabled{background:#ccc;cursor:not-allowed;transform:none;box-shadow:none}
.spinner{width:16px;height:16px;border:2.5px solid rgba(255,255,255,.3);border-top-color:#fff;border-radius:50%%;animation:spin .7s linear infinite;display:none}
@keyframes spin{to{transform:rotate(360deg)}}
.btn.loading .spinner{display:block}
.btn.loading .btn-text{opacity:.7}
.toast{position:fixed;top:16px;left:50%%;transform:translateX(-50%%) translateY(-100px);background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:10px;font-size:13px;font-weight:600;box-shadow:0 8px 24px rgba(0,0,0,.2);transition:transform .3s cubic-bezier(.4,0,.2,1);z-index:9999;white-space:nowrap;max-width:90vw}
.toast.show{transform:translateX(-50%%) translateY(0)}
.toast.error{background:#c62828}
.success-overlay{text-align:center;padding:8px 0;animation:fadeIn .3s ease}
.success-circle{width:72px;height:72px;background:linear-gradient(135deg,#1a1a1a,#333);border-radius:50%%;display:flex;align-items:center;justify-content:center;margin:0 auto 16px;font-size:32px;color:#fff}
.success-title{font-size:22px;font-weight:800;margin-bottom:6px}
.success-sub{font-size:14px;color:#888;margin-bottom:22px}
.success-detail{background:#f9f9f9;border-radius:12px;padding:16px;text-align:left;margin-bottom:22px}
.detail-row{display:flex;justify-content:space-between;align-items:center;font-size:13px;padding:7px 0;border-bottom:1px solid #f0f0f0}
.detail-row:last-child{border:none}
.detail-k{color:#888}
.detail-v{font-weight:700;color:#000}
.detail-v.green{color:#2e7d32}
.done-btn{padding:14px 40px;background:#000;color:#fff;border-radius:12px;font-size:15px;font-weight:700;border:none;cursor:pointer;transition:background .2s}
.done-btn:hover{background:#333}
@media(max-width:440px){body{padding:0;align-items:flex-start}.checkout-card{border-radius:0;min-height:100vh;max-width:100%%;box-shadow:none}.tab{font-size:12px}}
</style>
</head>
<body>
<div id="toast" class="toast"></div>
<div class="checkout-card">
  <div class="header">
    <div class="header-row">
      <div>
        <div class="brand-name">RideGo</div>
        <div class="brand-sub">Trip Payment</div>
      </div>
      <div class="fare-block">
        <div class="fare-amount">&#8377;%.2f</div>
        <div class="fare-label">Amount due</div>
      </div>
    </div>
    <div class="progress-bar">
      <div class="dot done"></div>
      <div class="dot done"></div>
      <div class="dot active"></div>
      <span class="progress-label">Payment</span>
    </div>
  </div>

  <div class="tabs-wrapper">
    <div class="tabs">
      <div class="tab" id="tab-cash" onclick="switchTab('cash')"><span class="tab-icon">&#128181;</span>Cash</div>
      <div class="tab" id="tab-upi"  onclick="switchTab('upi')"><span class="tab-icon">&#9889;</span>UPI</div>
      <div class="tab" id="tab-card" onclick="switchTab('card')"><span class="tab-icon">&#128179;</span>Card</div>
    </div>
  </div>

  <div class="body">

    <!-- CASH -->
    <div id="panel-cash" class="panel">
      <div class="method-title">Pay with Cash</div>
      <div class="method-sub">Hand your driver the exact amount</div>
      <ul class="steps">
        <li class="step"><div class="step-num">1</div><div class="step-text">Prepare <strong>&#8377;%.2f</strong> in cash before your trip ends</div></li>
        <li class="step"><div class="step-num">2</div><div class="step-text">Hand the cash directly to your driver</div></li>
        <li class="step"><div class="step-num">3</div><div class="step-text">Driver confirms receipt on their app &mdash; this page updates automatically</div></li>
      </ul>
      <div class="waiting-box">
        <div class="pulse-ring"></div>
        <div class="waiting-title">Awaiting driver confirmation</div>
        <div class="waiting-sub">This page will update once your driver confirms</div>
      </div>
    </div>

    <!-- UPI -->
    <div id="panel-upi" class="panel">
      <div class="method-title">Pay via UPI</div>
      <div class="method-sub">Use any UPI app you prefer</div>
      <div class="upi-apps">
        <div class="upi-app"><div class="upi-app-icon gpay">G Pay</div><span>GPay</span></div>
        <div class="upi-app"><div class="upi-app-icon phonepe">&#9654;</div><span>PhonePe</span></div>
        <div class="upi-app"><div class="upi-app-icon paytm">Pay</div><span>Paytm</span></div>
        <div class="upi-app"><div class="upi-app-icon bhim">B</div><span>BHIM</span></div>
      </div>
      <div class="test-hint">
        <span class="hint-label">&#127381; Test VPA</span>
        <span class="hint-value">success@razorpay</span>
      </div>
      <button class="btn" id="btn-upi" onclick="payUPI()">
        <div class="spinner"></div>
        <span class="btn-text">Pay &#8377;%.2f via UPI</span>
      </button>
    </div>

    <!-- CARD -->
    <div id="panel-card" class="panel">
      <div class="method-title">Pay with Card</div>
      <div class="method-sub">Secured by Razorpay &mdash; we never store your card details</div>
      <div class="card-logos">
        <div class="card-logo">VISA</div>
        <div class="card-logo">MC</div>
        <div class="card-logo">RuPay</div>
        <div class="card-logo">AMEX</div>
      </div>
      <div class="test-card-box">
        <div class="test-card-label">&#127381; Demo Test Card</div>
        <div class="test-card-num">4100 2800 0000 1007</div>
        <div class="test-card-sub">Any future expiry &nbsp;&middot;&nbsp; Any CVV &nbsp;&middot;&nbsp; Any name</div>
        <button class="copy-btn" onclick="copyCard()">&#128203; Copy number</button>
      </div>
      <button class="btn" id="btn-card" onclick="payCard()">
        <div class="spinner"></div>
        <span class="btn-text">Pay &#8377;%.2f with Card</span>
      </button>
    </div>

  </div>
</div>

<script>
const PAYMENT_ID=%q;
const TOKEN=%q;
const TRIP_ID=%q;
const AMOUNT=%.2f;
const RZP_KEY=%q;
const RZP_ORDER=%q;
const WS_PATH=%q;
const METHOD=%q;

const wsProto=location.protocol==='https:'?'wss:':'ws:';
const ws=new WebSocket(wsProto+'//'+location.host+WS_PATH+'?token='+TOKEN);
ws.onmessage=function(e){var d=JSON.parse(e.data);if(d.status==='completed')showSuccess('online',AMOUNT);};

function showToast(msg,type){
  var t=document.getElementById('toast');
  t.textContent=msg;
  t.className='toast'+(type==='error'?' error':'');
  t.classList.add('show');
  setTimeout(function(){t.classList.remove('show');},3000);
}

function switchTab(tab){
  ['cash','upi','card'].forEach(function(n){
    document.getElementById('tab-'+n).classList.toggle('active',n===tab);
    document.getElementById('panel-'+n).classList.toggle('active',n===tab);
  });
}
switchTab(METHOD==='cash'?'cash':METHOD==='card'?'card':'upi');

function setLoading(id,on){var b=document.getElementById(id);b.disabled=on;b.classList.toggle('loading',on);}

function openRazorpay(method,btnID){
  setLoading(btnID,true);
  new Razorpay({
    key:RZP_KEY,amount:Math.round(AMOUNT*100),currency:'INR',
    order_id:RZP_ORDER,name:'RideGo',description:'Trip Payment',
    prefill:{method:method},theme:{color:'#000000'},
    modal:{ondismiss:function(){setLoading(btnID,false);}},
    handler:async function(resp){
      try{
        var r=await fetch('/payments/verify',{
          method:'POST',
          headers:{'Content-Type':'application/json','Authorization':'Bearer '+TOKEN},
          body:JSON.stringify({
            payment_id:PAYMENT_ID,
            provider_order_id:resp.razorpay_order_id,
            provider_payment_id:resp.razorpay_payment_id,
            signature:resp.razorpay_signature
          })
        });
        var d=await r.json();
        if(d.error)throw new Error(d.error);
        showSuccess(method,AMOUNT);
      }catch(e){setLoading(btnID,false);showToast('Payment failed: '+e.message,'error');}
    }
  }).open();
}

function payUPI(){openRazorpay('upi','btn-upi');}
function payCard(){openRazorpay('card','btn-card');}

function copyCard(){
  navigator.clipboard.writeText('4100280000001007').then(function(){showToast('Card number copied!');});
}

function showSuccess(method,amount){
  ws.close();
  var labels={cash:'Cash',card:'Credit/Debit Card',upi:'UPI',online:'Online'};
  document.querySelector('.tabs-wrapper').style.display='none';
  document.querySelector('.body').innerHTML=
    '<div class="success-overlay">'+
    '<div class="success-circle">&#10004;</div>'+
    '<div class="success-title">Payment Successful</div>'+
    '<div class="success-sub">Your trip payment has been confirmed.</div>'+
    '<div class="success-detail">'+
    '<div class="detail-row"><span class="detail-k">Amount Paid</span><span class="detail-v">&#8377;'+(+amount).toFixed(2)+'</span></div>'+
    '<div class="detail-row"><span class="detail-k">Method</span><span class="detail-v">'+(labels[method]||method)+'</span></div>'+
    '<div class="detail-row"><span class="detail-k">Trip ID</span><span class="detail-v" style="font-size:11px">'+TRIP_ID.substring(0,8)+'...</span></div>'+
    '<div class="detail-row"><span class="detail-k">Status</span><span class="detail-v green">&#10003; COMPLETED</span></div>'+
    '</div>'+
    '<button class="done-btn" onclick="window.close()">Done</button>'+
    '</div>';
}
</script>
</body>
</html>`,
		amount,    // header fare
		amount,    // cash step 1
		amount,    // UPI button
		amount,    // card button
		paymentID, token, tripID, amount, keyID, providerOrderID, wsPath, paymentMethod)
}

func renderSuccess(amount float64, method, tripID string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"/><title>RideGo — Payment Complete</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:linear-gradient(135deg,#f0f0f0 0%%,#e4e4e4 100%%);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:16px}
.card{background:#fff;border-radius:20px;box-shadow:0 20px 60px rgba(0,0,0,.15),0 4px 16px rgba(0,0,0,.08);width:100%%;max-width:420px;overflow:hidden}
.header{background:linear-gradient(135deg,#0f0f0f 0%%,#2d2d2d 60%%,#1a1a1a 100%%);padding:24px 28px}
.logo{color:#fff;font-size:22px;font-weight:800;letter-spacing:-.5px}
.body{padding:36px 28px;text-align:center}
.circle{width:72px;height:72px;background:linear-gradient(135deg,#1a1a1a,#333);border-radius:50%%;display:flex;align-items:center;justify-content:center;margin:0 auto 16px;font-size:32px;color:#fff}
.title{font-size:22px;font-weight:800;margin-bottom:8px}
.sub{font-size:14px;color:#888;margin-bottom:24px}
.detail{background:#f9f9f9;border-radius:12px;padding:16px;text-align:left;margin-bottom:24px}
.row{display:flex;justify-content:space-between;align-items:center;font-size:13px;padding:7px 0;border-bottom:1px solid #f0f0f0}
.row:last-child{border:none}
.k{color:#888}.v{font-weight:700;color:#000}.v.green{color:#2e7d32}
.btn{padding:14px 40px;background:#000;color:#fff;border-radius:12px;font-size:15px;font-weight:700;border:none;cursor:pointer;transition:background .2s}
.btn:hover{background:#333}
@media(max-width:440px){body{padding:0;align-items:flex-start}.card{border-radius:0;min-height:100vh;max-width:100%%;box-shadow:none}}
</style>
</head><body>
<div class="card">
  <div class="header"><div class="logo">RideGo</div></div>
  <div class="body">
    <div class="circle">&#10004;</div>
    <div class="title">Payment Successful</div>
    <div class="sub">Your trip payment has been confirmed.</div>
    <div class="detail">
      <div class="row"><span class="k">Amount Paid</span><span class="v">&#8377;%.2f</span></div>
      <div class="row"><span class="k">Method</span><span class="v">%s</span></div>
      <div class="row"><span class="k">Trip ID</span><span class="v" style="font-size:11px">%s</span></div>
      <div class="row"><span class="k">Status</span><span class="v green">&#10003; COMPLETED</span></div>
    </div>
    <button class="btn" onclick="window.close()">Done</button>
  </div>
</div>
</body></html>`, amount, method, tripID)
}
