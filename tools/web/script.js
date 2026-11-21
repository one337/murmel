(function(){
  var status=document.getElementById("copy-status");
  function say(text){
    if(!status)return;
    status.textContent="";
    setTimeout(function(){status.textContent=text},50);
  }
  document.querySelectorAll(".code .copy").forEach(function(b){
    b.addEventListener("click",function(){
      var text=b.parentElement.querySelector("pre").innerText;
      var copy=navigator.clipboard&&navigator.clipboard.writeText?
        navigator.clipboard.writeText(text):Promise.reject(new Error("no clipboard"));
      copy.then(function(){
        b.textContent="Copied";b.classList.add("done");
        say("Copied to the clipboard.");
        setTimeout(function(){b.textContent="Copy";b.classList.remove("done")},1500);
      },function(){
        say("Copying failed. Select the command and copy it by hand.");
      });
    });
  });

  // On small screens and touch devices the links open the full-size image,
  // where the browser can zoom; the viewer is for wide screens with a mouse.
  var wide=window.matchMedia("(min-width: 48rem) and (pointer: fine)");
  var dlg=document.getElementById("lightbox");
  if(!dlg||!dlg.showModal)return;
  var big=document.createElement("img"),cap=dlg.querySelector(".lb-cap");
  big.alt="";
  dlg.insertBefore(big,dlg.querySelector(".next"));
  var items=Array.prototype.slice.call(document.querySelectorAll(".zoom"));
  var cur=0;
  function show(i){
    cur=(i+items.length)%items.length;
    var img=items[cur].querySelector("img");
    big.src=items[cur].href;
    big.alt=img.alt;
    var fc=items[cur].parentElement.querySelector("figcaption");
    cap.textContent=fc?fc.textContent:img.alt;
  }
  items.forEach(function(a,i){a.addEventListener("click",function(e){
    if(!wide.matches)return;
    e.preventDefault();show(i);dlg.showModal();
  });});
  dlg.querySelector(".lb-close").addEventListener("click",function(){dlg.close();});
  dlg.querySelector(".prev").addEventListener("click",function(e){e.stopPropagation();show(cur-1);});
  dlg.querySelector(".next").addEventListener("click",function(e){e.stopPropagation();show(cur+1);});
  dlg.addEventListener("click",function(e){if(e.target===dlg||e.target===big)dlg.close();});
  dlg.addEventListener("keydown",function(e){
    if(e.key==="ArrowLeft")show(cur-1);
    if(e.key==="ArrowRight")show(cur+1);
  });
})();
